package lnwire

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"net"

	"github.com/go-errors/errors"
	"github.com/roasbeef/btcd/btcec"
	"github.com/roasbeef/btcd/chaincfg/chainhash"
	"github.com/roasbeef/btcd/wire"
	"github.com/roasbeef/btcutil"
)

// MaxSliceLength is the maximum allowed lenth for any opaque byte slices in
// the wire protocol.
const MaxSliceLength = 65535

// PkScript is simple type definition which represents a raw serialized public
// key script.
type PkScript []byte

// addressType specifies the network protocol and version that should be used
// when connecting to a node at a particular address.
type addressType uint8

const (
	tcp4Addr  addressType = 1
	tcp6Addr  addressType = 2
	onionAddr addressType = 3
)

// writeElement is a one-stop shop to write the big endian representation of
// any element which is to be serialized for the wire protocol. The passed
// io.Writer should be backed by an appropriately sized byte slice, or be able
// to dynamically expand to accommodate additional data.
//
// TODO(roasbeef): this should eventually draw from a buffer pool for
// serialization.
// TODO(roasbeef): switch to var-ints for all?
func writeElement(w io.Writer, element interface{}) error {
	switch e := element.(type) {
	case uint8:
		var b [1]byte
		b[0] = e
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case uint16:
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], e)
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case ErrorCode:
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(e))
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case btcutil.Amount:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(e))
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case uint32:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], e)
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case uint64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], e)
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case *btcec.PublicKey:
		var b [33]byte
		serializedPubkey := e.SerializeCompressed()
		copy(b[:], serializedPubkey)
		// TODO(roasbeef): use WriteVarBytes here?
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
	case []*btcec.Signature:
		var b [2]byte
		numSigs := uint16(len(e))
		binary.BigEndian.PutUint16(b[:], numSigs)
		if _, err := w.Write(b[:]); err != nil {
			return err
		}

		for _, sig := range e {
			if err := writeElement(w, sig); err != nil {
				return err
			}
		}
	case *btcec.Signature:
		var b [64]byte
		err := serializeSigToWire(&b, e)
		if err != nil {
			return err
		}
		// Write buffer
		if _, err = w.Write(b[:]); err != nil {
			return err
		}
	case PingPayload:
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(e)))
		if _, err := w.Write(l[:]); err != nil {
			return err
		}

		if _, err := w.Write(e[:]); err != nil {
			return err
		}
	case PongPayload:
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(e)))
		if _, err := w.Write(l[:]); err != nil {
			return err
		}

		if _, err := w.Write(e[:]); err != nil {
			return err
		}
	case ErrorData:
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(e)))
		if _, err := w.Write(l[:]); err != nil {
			return err
		}

		if _, err := w.Write(e[:]); err != nil {
			return err
		}
	case OpaqueReason:
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(e)))
		if _, err := w.Write(l[:]); err != nil {
			return err
		}

		if _, err := w.Write(e[:]); err != nil {
			return err
		}
	case []byte:
		if _, err := w.Write(e[:]); err != nil {
			return err
		}
	case PkScript:
		// The largest script we'll accept is a p2wsh which is exactly
		// 34 bytes long.
		scriptLength := len(e)
		if scriptLength > 34 {
			return fmt.Errorf("'PkScript' too long")
		}

		if err := wire.WriteVarBytes(w, 0, e); err != nil {
			return err
		}
	case *FeatureVector:
		if err := e.Encode(w); err != nil {
			return err
		}

	case wire.OutPoint:
		var h [32]byte
		copy(h[:], e.Hash[:])
		if _, err := w.Write(h[:]); err != nil {
			return err
		}

		if e.Index > math.MaxUint16 {
			return fmt.Errorf("index for outpoint (%v) is "+
				"greater than max index of %v", e.Index,
				math.MaxUint16)
		}

		var idx [2]byte
		binary.BigEndian.PutUint16(idx[:], uint16(e.Index))
		if _, err := w.Write(idx[:]); err != nil {
			return err
		}

	case ChannelID:
		if _, err := w.Write(e[:]); err != nil {
			return err
		}
	case FailCode:
		if err := writeElement(w, uint16(e)); err != nil {
			return err
		}
	case ShortChannelID:
		// Check that field fit in 3 bytes and write the blockHeight
		if e.BlockHeight > ((1 << 24) - 1) {
			return errors.New("block height should fit in 3 bytes")
		}

		var blockHeight [4]byte
		binary.BigEndian.PutUint32(blockHeight[:], e.BlockHeight)

		if _, err := w.Write(blockHeight[1:]); err != nil {
			return err
		}

		// Check that field fit in 3 bytes and write the txIndex
		if e.TxIndex > ((1 << 24) - 1) {
			return errors.New("tx index should fit in 3 bytes")
		}

		var txIndex [4]byte
		binary.BigEndian.PutUint32(txIndex[:], e.TxIndex)
		if _, err := w.Write(txIndex[1:]); err != nil {
			return err
		}

		// Write the txPosition
		var txPosition [2]byte
		binary.BigEndian.PutUint16(txPosition[:], e.TxPosition)
		if _, err := w.Write(txPosition[:]); err != nil {
			return err
		}

	case *net.TCPAddr:
		if e.IP.To4() != nil {
			var descriptor [1]byte
			descriptor[0] = uint8(tcp4Addr)
			if _, err := w.Write(descriptor[:]); err != nil {
				return err
			}

			var ip [4]byte
			copy(ip[:], e.IP.To4())
			if _, err := w.Write(ip[:]); err != nil {
				return err
			}
		} else {
			var descriptor [1]byte
			descriptor[0] = uint8(tcp6Addr)
			if _, err := w.Write(descriptor[:]); err != nil {
				return err
			}
			var ip [16]byte
			copy(ip[:], e.IP.To16())
			if _, err := w.Write(ip[:]); err != nil {
				return err
			}
		}
		var port [2]byte
		binary.BigEndian.PutUint16(port[:], uint16(e.Port))
		if _, err := w.Write(port[:]); err != nil {
			return err
		}
	case []net.Addr:
		// Write out the number of addresses.
		if err := writeElement(w, uint16(len(e))); err != nil {
			return err
		}

		// Append the actual addresses.
		for _, address := range e {
			if err := writeElement(w, address); err != nil {
				return err
			}
		}
	case RGB:
		err := writeElements(w,
			e.red,
			e.green,
			e.blue,
		)
		if err != nil {
			return err
		}
	case Alias:
		if err := writeElements(w, e.data[:]); err != nil {
			return err
		}
	case DeliveryAddress:
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(e)))
		if _, err := w.Write(length[:]); err != nil {
			return err
		}
		if _, err := w.Write(e[:]); err != nil {
			return err
		}

	default:
		return fmt.Errorf("Unknown type in writeElement: %T", e)
	}

	return nil
}

// writeElements is writes each element in the elements slice to the passed
// io.Writer using writeElement.
func writeElements(w io.Writer, elements ...interface{}) error {
	for _, element := range elements {
		err := writeElement(w, element)
		if err != nil {
			return err
		}
	}
	return nil
}

// readElement is a one-stop utility function to deserialize any datastructure
// encoded using the serialization format of lnwire.
func readElement(r io.Reader, element interface{}) error {
	var err error
	switch e := element.(type) {
	case *uint8:
		var b [1]uint8
		if _, err := r.Read(b[:]); err != nil {
			return err
		}
		*e = b[0]
	case *uint16:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return err
		}
		*e = binary.BigEndian.Uint16(b[:])
	case *ErrorCode:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return err
		}
		*e = ErrorCode(binary.BigEndian.Uint16(b[:]))
	case *uint32:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return err
		}
		*e = binary.BigEndian.Uint32(b[:])
	case *uint64:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return err
		}
		*e = binary.BigEndian.Uint64(b[:])
	case *btcutil.Amount:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return err
		}
		*e = btcutil.Amount(int64(binary.BigEndian.Uint64(b[:])))
	case **btcec.PublicKey:
		var b [btcec.PubKeyBytesLenCompressed]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return err
		}

		pubKey, err := btcec.ParsePubKey(b[:], btcec.S256())
		if err != nil {
			return err
		}
		*e = pubKey
	case **FeatureVector:
		f, err := NewFeatureVectorFromReader(r)
		if err != nil {
			return err
		}

		*e = f

	case *[]*btcec.Signature:
		var l [2]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return err
		}
		numSigs := binary.BigEndian.Uint16(l[:])

		var sigs []*btcec.Signature
		if numSigs > 0 {
			sigs = make([]*btcec.Signature, numSigs)
			for i := 0; i < int(numSigs); i++ {
				if err := readElement(r, &sigs[i]); err != nil {
					return err
				}
			}
		}

		*e = sigs

	case **btcec.Signature:
		var b [64]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return err
		}
		err = deserializeSigFromWire(e, b)
		if err != nil {
			return err
		}
	case *OpaqueReason:
		var l [2]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return err
		}
		reasonLen := binary.BigEndian.Uint16(l[:])

		*e = OpaqueReason(make([]byte, reasonLen))
		if _, err := io.ReadFull(r, *e); err != nil {
			return err
		}
	case *ErrorData:
		var l [2]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return err
		}
		errorLen := binary.BigEndian.Uint16(l[:])

		*e = ErrorData(make([]byte, errorLen))
		if _, err := io.ReadFull(r, *e); err != nil {
			return err
		}
	case *PingPayload:
		var l [2]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return err
		}
		pingLen := binary.BigEndian.Uint16(l[:])

		*e = PingPayload(make([]byte, pingLen))
		if _, err := io.ReadFull(r, *e); err != nil {
			return err
		}
	case *PongPayload:
		var l [2]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return err
		}
		pongLen := binary.BigEndian.Uint16(l[:])

		*e = PongPayload(make([]byte, pongLen))
		if _, err := io.ReadFull(r, *e); err != nil {
			return err
		}
	case []byte:
		if _, err := io.ReadFull(r, e); err != nil {
			return err
		}
	case *PkScript:
		pkScript, err := wire.ReadVarBytes(r, 0, 34, "pkscript")
		if err != nil {
			return err
		}
		*e = pkScript
	case *wire.OutPoint:
		var h [32]byte
		if _, err = io.ReadFull(r, h[:]); err != nil {
			return err
		}
		hash, err := chainhash.NewHash(h[:])
		if err != nil {
			return err
		}

		var idxBytes [2]byte
		_, err = io.ReadFull(r, idxBytes[:])
		if err != nil {
			return err
		}
		index := binary.BigEndian.Uint16(idxBytes[:])

		*e = wire.OutPoint{
			Hash:  *hash,
			Index: uint32(index),
		}
	case *FailCode:
		if err := readElement(r, (*uint16)(e)); err != nil {
			return err
		}

	case *ChannelID:
		if _, err := io.ReadFull(r, e[:]); err != nil {
			return err
		}

	case *ShortChannelID:
		var blockHeight [4]byte
		if _, err = io.ReadFull(r, blockHeight[1:]); err != nil {
			return err
		}

		var txIndex [4]byte
		if _, err = io.ReadFull(r, txIndex[1:]); err != nil {
			return err
		}

		var txPosition [2]byte
		if _, err = io.ReadFull(r, txPosition[:]); err != nil {
			return err
		}

		*e = ShortChannelID{
			BlockHeight: binary.BigEndian.Uint32(blockHeight[:]),
			TxIndex:     binary.BigEndian.Uint32(txIndex[:]),
			TxPosition:  binary.BigEndian.Uint16(txPosition[:]),
		}

	case *[]net.Addr:
		var numAddrsBytes [2]byte
		if _, err = io.ReadFull(r, numAddrsBytes[:]); err != nil {
			return err
		}

		numAddrs := binary.BigEndian.Uint16(numAddrsBytes[:])
		addresses := make([]net.Addr, 0, numAddrs)

		for i := 0; i < int(numAddrs); i++ {
			var descriptor [1]byte
			if _, err = io.ReadFull(r, descriptor[:]); err != nil {
				return err
			}

			address := &net.TCPAddr{}
			switch descriptor[0] {
			case 1:
				var ip [4]byte
				if _, err = io.ReadFull(r, ip[:]); err != nil {
					return err
				}
				address.IP = (net.IP)(ip[:])
			case 2:
				var ip [16]byte
				if _, err = io.ReadFull(r, ip[:]); err != nil {
					return err
				}
				address.IP = (net.IP)(ip[:])
			}

			var port [2]byte
			if _, err = io.ReadFull(r, port[:]); err != nil {
				return err
			}

			address.Port = int(binary.BigEndian.Uint16(port[:]))
			addresses = append(addresses, address)
		}
		*e = addresses
	case *RGB:
		err := readElements(r,
			&e.red,
			&e.green,
			&e.blue,
		)
		if err != nil {
			return err
		}
	case *Alias:
		var a [32]byte
		if err := readElements(r, a[:]); err != nil {
			return err
		}

		*e = newAlias(a[:])
	case *DeliveryAddress:
		var addrLen [2]byte
		if _, err = io.ReadFull(r, addrLen[:]); err != nil {
			return err
		}
		length := binary.BigEndian.Uint16(addrLen[:])

		var addrBytes [34]byte
		if _, err = io.ReadFull(r, addrBytes[:length]); err != nil {
			return err
		}
		*e = addrBytes[:length]
	default:
		return fmt.Errorf("Unknown type in readElement: %T", e)
	}

	return nil
}

// readElements deserializes a variable number of elements into the passed
// io.Reader, with each element being deserialized according to the readElement
// function.
func readElements(r io.Reader, elements ...interface{}) error {
	for _, element := range elements {
		err := readElement(r, element)
		if err != nil {
			return err
		}
	}
	return nil
}

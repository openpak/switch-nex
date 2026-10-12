// SPDX-License-Identifier: AGPL-3.0-only
// Eagle v2 uses a big-endian bit stream, independent of NEX's byte stream.
// Wire facts: Kinnay's Eagle Protocol / Eagle RPC documentation (provenance.md).
package eagle

import "errors"

const nodeBits = 11
const nodeLimit = 1024
const maxPacket = 65535

var errPacket = errors.New("invalid Eagle packet")

type bitsIn struct {
	data []byte
	pos  int
}

func (r *bitsIn) take(n int) (uint64, error) {
	if n < 0 || n > 64 || r.pos+n > len(r.data)*8 {
		return 0, errPacket
	}
	var value uint64
	for range n {
		value = value<<1 | uint64((r.data[r.pos/8]>>uint(7-r.pos%8))&1)
		r.pos++
	}
	return value, nil
}

type bitsOut struct {
	data []byte
	pos  int
}

func (w *bitsOut) put(value uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.pos/8 == len(w.data) {
			w.data = append(w.data, 0)
		}
		w.data[w.pos/8] |= byte((value>>uint(i))&1) << uint(7-w.pos%8)
		w.pos++
	}
}

type packet struct {
	op      uint8
	targets []uint16
	payload []byte
}

func parsePacket(data []byte) (packet, error) {
	var p packet
	if len(data) > maxPacket {
		return p, errPacket
	}
	r := bitsIn{data: data}
	relay, err := r.take(2)
	if err != nil || relay > 2 {
		return p, errPacket
	}
	op, err := r.take(8)
	if err != nil {
		return p, err
	}
	p.op = uint8(op)
	if _, err = r.take(nodeBits); err != nil { // sender is assigned by the server
		return p, err
	}
	if relay == 1 {
		target, err := r.take(nodeBits)
		if err != nil {
			return p, err
		}
		if target > nodeLimit+1 {
			return p, errPacket
		}
		p.targets = append(p.targets, uint16(target))
	}
	if relay == 2 {
		for i := range nodeLimit {
			bit, err := r.take(1)
			if err != nil {
				return p, err
			}
			if bit != 0 {
				p.targets = append(p.targets, uint16(i))
			}
		}
	}
	start := (r.pos + 7) / 8
	if start > len(data) {
		return p, errPacket
	}
	p.payload = data[start:]
	return p, nil
}

func encodePacket(op uint8, source uint16, payload []byte) []byte {
	w := bitsOut{}
	w.put(0, 2) // server deliveries have no relay destinations
	w.put(uint64(op), 8)
	w.put(uint64(source), nodeBits)
	return append(w.data, payload...)
}

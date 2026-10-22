// Messaging (0x17) / MessageDelivery (0x1B) and Luigi's Mansion 3's title
// presence protocol (0x79).
//
// DeliverMessage (0x1B.0x1) is the SMB35 message pipe, and a relay: the
// server answers nothing (the method is no-response) and pushes the message on
// to its recipients — one principal, or every participant of a gathering, the
// sender included. SMB35's host announces its lobby state through it
// (participants_cnt, close_participation); acknowledged and dropped, the host
// counted 2/2 while its joiner sat at 1/1 and the match never started (sweep
// 2026-10-10). The 0x79 stub acknowledges exactly the four
// methods LM3 calls — an unacknowledged call (Core::NotImplemented) is the
// answer most likely to hard-stall the session — and answers the two station
// queries with an empty record list: "no friends have a room open".
package protocols

import (
	"fmt"
	"log"
	"strconv"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func messagingHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.MethodID == 0x01 { // DeliverMessage
		return success(msg, nil)
	}
	return notImplemented(env, msg)
}

func messageDeliveryHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.MethodID == 0x01 { // DeliverMessage(AnyDataHolder<UserMessage>) -> no response
		relayMessage(env, c, msg.Body)
		return nil
	}
	if msg.MethodID == 0x02 { // DeliverMessageMultiTarget: never seen on the wire
		return success(msg, nil)
	}
	return notImplemented(env, msg)
}

// Message recipient types (MessageRecipient.m_uiRecipientType).
const (
	recipientPrincipal uint32 = 1
	recipientGathering uint32 = 2
)

// relayMessage stamps a delivered message with its sender and reception time,
// as the working server does, and pushes it to its recipients. A gathering message
// from someone who is not in the gathering goes nowhere.
func relayMessage(env *Env, c *prudplite.Connection, body []byte) {
	name, out, typ, pid, gid, err := restampMessage(body, c.PID, nowDateTime())
	if err != nil {
		log.Printf("[%s] message: undecodable delivery (%dB): %v", env.Name, len(body), err)
		return
	}
	var to []uint64
	switch typ {
	case recipientPrincipal:
		to = []uint64{pid}
	case recipientGathering:
		if env.Match == nil {
			break
		}
		if g, ok := env.Match.get(gid); ok && g.participants[c.PID] {
			to = participantPIDs(g)
		}
	}
	sent := 0
	for _, target := range to {
		conn, ok := env.push.connection(target)
		if !ok {
			continue
		}
		req := rmc.EncodeRequest(ProtoMessageDelivery, 0x01, env.push.nextCallID(), out)
		if err := conn.SendRMC(req); err == nil {
			sent++
		}
	}
	log.Printf("[%s] message: %s to type=%d gid=%d relayed to %d of %d recipient(s)", env.Name, name, typ, gid, sent, len(to))
}

// restampMessage rewrites a DeliverMessage body (an AnyDataHolder around a
// UserMessage subclass) with the server-owned fields filled in: sender,
// sender name and reception time. It returns the class name, the rewritten
// body and the recipient. Every level after UserMessage (the BinaryMessage
// or TextMessage body) is carried over byte for byte.
//
// Wire (SMB35, 2026-10-09): String class, u32 size+4, Buffer{ Data header
// (empty), UserMessage header + {u32 id, u32 parent, u64 sender, u64
// reception, u32 lifetime, u32 flags, String subject, String sender name,
// MessageRecipient{u32 type, u64 pid, u32 gid}}, subclass level }.
func restampMessage(body []byte, sender, now uint64) (name string, out []byte, typ uint32, pid uint64, gid uint32, err error) {
	r := rmc.NewReader(body)
	if name, err = r.String(); err != nil {
		return
	}
	if _, err = r.U32(); err != nil {
		return
	}
	inner, err := r.Buffer()
	if err != nil {
		return
	}
	ir := rmc.NewReader(inner)
	dataVer, data, err := ir.Struct() // the empty Data base level
	if err != nil {
		return
	}
	dataBytes := make([]byte, data.Remaining())
	for i := range dataBytes {
		dataBytes[i], _ = data.U8()
	}
	umVer, um, err := ir.Struct()
	if err != nil {
		return
	}
	rest := inner[len(inner)-ir.Remaining():]

	var id, parent, lifetime, flags uint32
	var subject string
	fail := func(e error) bool {
		if e != nil && err == nil {
			err = e
		}
		return err != nil
	}
	var e error
	id, e = um.U32()
	fail(e)
	parent, e = um.U32()
	fail(e)
	_, e = um.U64() // sender: the server's to set
	fail(e)
	_, e = um.U64() // reception time: likewise
	fail(e)
	lifetime, e = um.U32()
	fail(e)
	flags, e = um.U32()
	fail(e)
	subject, e = um.String()
	fail(e)
	_, e = um.String() // sender name
	fail(e)
	recipVer, recip, e := um.Struct()
	if fail(e) {
		return
	}
	typ, e = recip.U32()
	fail(e)
	pid, e = recip.U64()
	fail(e)
	gid, e = recip.U32()
	if fail(e) {
		return
	}
	if um.Remaining() != 0 {
		err = fmt.Errorf("rmc: %d unread UserMessage byte(s)", um.Remaining())
		return
	}

	w := rmc.NewWriter()
	w.Struct(dataVer, dataBytes)
	w.StructOf(umVer, func(w *rmc.Writer) {
		w.U32(id)
		w.U32(parent)
		w.U64(sender)
		w.U64(now)
		w.U32(lifetime)
		w.U32(flags)
		w.String(subject)
		w.String(strconv.FormatUint(sender, 10))
		w.StructOf(recipVer, func(w *rmc.Writer) {
			w.U32(typ)
			w.U64(pid)
			w.U32(gid)
		})
	})
	w.Raw(rest)
	holder := rmc.NewWriter()
	holder.String(name)
	holder.U32(uint32(len(w.Bytes()) + 4))
	holder.Buffer(w.Bytes())
	return name, holder.Bytes(), typ, pid, gid, nil
}

// presenceHandler serves the 0x79 station/presence protocol:
//
//	0x05 UpdateApplicationBuffer  publish-only -> empty
//	0x0D UpdateState              publish-only -> empty
//	0x0E InitializeStation        -> empty record list
//	0x0F GetStationLocation       -> empty record list
func presenceHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x05, 0x0D:
		return success(msg, nil)
	case 0x0E, 0x0F:
		return emptyList(msg)
	default:
		return notImplemented(env, msg)
	}
}

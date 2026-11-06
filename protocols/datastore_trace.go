// DataStore call tracing for titles whose DataStore methods are not yet mapped
// (Super Smash Bros. Ultimate: the wiki lists methods 47-84 as unknown). It logs
// every call, including those a stub answers, so one console session records
// what the game sends. Bodies are masked: the caller's own PID and printable
// text runs never reach the log. Off unless SWITCH_NEX_TRACE_DATASTORE names the title.
package protocols

import (
	"encoding/binary"
	"fmt"
	"log"
	"strings"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

const traceMaxBytes = 4096

func tracedDataStore(next func(*Env, *prudplite.Connection, *rmc.RMCMessage) []byte) func(*Env, *prudplite.Connection, *rmc.RMCMessage) []byte {
	return func(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
		resp := next(env, c, msg)
		var pid uint64
		if c != nil {
			pid = c.PID
		}
		log.Printf("[%s] datastore-trace method=0x%X call=%d req=%dB %s resp=%dB %s", env.Name,
			msg.MethodID, msg.CallID, len(msg.Body), maskBody(msg.Body, pid), len(resp), maskBody(resp, pid))
		return resp
	}
}

// maskBody renders b as hex with the caller's PID (u32/u64 little endian) shown
// as [pid] and printable-ASCII runs of six or more bytes shown as [str:N].
func maskBody(b []byte, pid uint64) string {
	trunc := len(b) > traceMaxBytes
	if trunc {
		b = b[:traceMaxBytes]
	}
	p32 := make([]byte, 4)
	binary.LittleEndian.PutUint32(p32, uint32(pid))
	var sb strings.Builder
	for i := 0; i < len(b); {
		if pid != 0 && i+4 <= len(b) && string(b[i:i+4]) == string(p32) {
			sb.WriteString("[pid]")
			i += 4
			continue
		}
		j := i
		for j < len(b) && b[j] >= 0x20 && b[j] < 0x7f {
			j++
		}
		if j-i >= 6 {
			fmt.Fprintf(&sb, "[str:%d]", j-i)
			i = j
			continue
		}
		fmt.Fprintf(&sb, "%02x", b[i])
		i++
	}
	if trunc {
		sb.WriteString("...")
	}
	return sb.String()
}

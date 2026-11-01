package protocols

import (
	"testing"

	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func TestSplatoon2Festival(t *testing.T) {
	env := NewEnv("splatoon-2", "127.0.0.1", 22320)
	c := &prudplite.Connection{PID: 7}
	call := func(m uint32, body []byte) ([]byte, bool) {
		return rankingSplatoon2(env, c, &rmc.RMCMessage{Protocol: 0x70, MethodID: m, Body: body})
	}
	vote := rmc.NewWriter()
	vote.StructOf(0, func(w *rmc.Writer) { w.U32(9); w.U8(1) })
	if _, ok := call(methodRankingS2UploadFestivalVote, vote.Bytes()); !ok {
		t.Fatal("vote not handled")
	}
	call(methodRankingS2UploadFestivalVote, vote.Bytes()) // repeat vote: still one participant
	score := rmc.NewWriter()
	score.StructOf(0, func(w *rmc.Writer) {
		w.U64(1); w.U8(0); w.U32(9); w.U8(1); w.U32(500); w.U32(0); w.U32(0); w.QBuffer(nil)
	})
	call(methodRankingS2UploadFestivalScore, score.Bytes())
	body := env.festivals.festivalResult(9)
	r := rmc.NewReader(body)
	_, in, err := r.Struct()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := in.U32()
	n, _ := in.U32()
	np, _ := in.ListHeader()
	var parts [3]uint32
	for i := range parts {
		parts[i], _ = in.U32()
	}
	_, _ = in.ListHeader()
	var sc [3]uint32
	for i := range sc {
		sc[i], _ = in.U32()
	}
	if id != 9 || n != 1 || np != 3 || parts != [3]uint32{0, 1, 0} || sc != [3]uint32{0, 500, 0} || in.Remaining() != 8 {
		t.Fatalf("result id=%d n=%d parts=%v scores=%v rest=%d", id, n, parts, sc, in.Remaining())
	}
	if _, ok := call(0x19, nil); ok {
		t.Fatal("GetEventMatchResult must stay unhandled")
	}
}

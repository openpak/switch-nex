package protocols

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

func smm2Env() *Env {
	e := testEnv()
	e.DataStore = DSSMM2
	return e
}

// getUsersBody is the console's GetUsers request (2026-10-10): a GetUsersParam
// structure, not a bare list, around one PID and result option 0xE284.
func getUsersBody(pid uint64) []byte {
	b, _ := hex.DecodeString("0010000000" + "01000000")
	b = binary.LittleEndian.AppendUint64(b, pid)
	return binary.LittleEndian.AppendUint32(b, 0xE284)
}

func registerUserBody(name string) []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.String(name)
		w.StructOf(0, func(w *rmc.Writer) { w.U16(1); w.U16(2); w.U16(3); w.U16(4) })
		w.QBuffer([]byte{0xAA, 0xBB, 0xCC})
		w.U8(1)
		w.String("GB")
		w.String("device")
	})
	return w.Bytes()
}

// TestSMM2GetUsersReadsTheConsolesRequest: the framed request used to fail its
// parse (InvalidArgument, "Couldn't retrieve uploaded courses"). An
// unregistered player is zero users and one NotFound, with the error bit set
// as a List<Result> carries it.
func TestSMM2GetUsersReadsTheConsolesRequest(t *testing.T) {
	env := smm2Env()
	_, body, errCode := decodeResponse(Dispatch(env, conn(77), request(ProtoDataStore, 0x30, getUsersBody(77))))
	if errCode != 0 {
		t.Fatalf("GetUsers: %#x", errCode)
	}
	if hex.EncodeToString(body) != "00000000"+"01000000"+"04006980" {
		t.Fatalf("GetUsers for a new player = %x", body)
	}
}

// TestSMM2RegisterThenGetUsers: the Mii picker registers, then asks again, and
// that answer must carry the profile, as a UserInfo the client reads to the end.
func TestSMM2RegisterThenGetUsers(t *testing.T) {
	env := smm2Env()
	if _, _, errCode := decodeResponse(Dispatch(env, conn(77), request(ProtoDataStore, 0x2F, registerUserBody("Maker")))); errCode != 0 {
		t.Fatalf("RegisterUser: %#x", errCode)
	}
	_, body, errCode := decodeResponse(Dispatch(env, conn(77), request(ProtoDataStore, 0x30, getUsersBody(77))))
	if errCode != 0 {
		t.Fatalf("GetUsers: %#x", errCode)
	}
	r := rmc.NewReader(body)
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatalf("users: %d", n)
	}
	readUserInfo(t, r, 77, "Maker")
	if n, _ := r.ListHeader(); n != 1 {
		t.Fatalf("results: %d", n)
	}
	if code, _ := r.U32(); code != 0x00010001 {
		t.Fatalf("result %#x", code)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d bytes left over", r.Remaining())
	}

	// An empty PID list asks for the caller's own profile.
	empty, _ := hex.DecodeString("0008000000" + "00000000" + "84e20000")
	_, body, _ = decodeResponse(Dispatch(env, conn(77), request(ProtoDataStore, 0x30, empty)))
	if n, _ := rmc.NewReader(body).ListHeader(); n != 1 {
		t.Fatal("an empty list did not return the caller's profile")
	}

	// SyncUserProfile answers the stored profile back.
	sync := rmc.NewWriter()
	sync.StructOf(0, func(w *rmc.Writer) {
		w.String("Maker2")
		w.StructOf(0, func(w *rmc.Writer) { w.U16(0); w.U16(0); w.U16(0); w.U16(0) })
		w.QBuffer(nil)
		w.U8(1)
		w.String("GB")
		w.Bool(true)
		w.Bool(true)
		w.String("guid")
		w.U32(0)
	})
	_, body, errCode = decodeResponse(Dispatch(env, conn(77), request(ProtoDataStore, 0x31, sync.Bytes())))
	if errCode != 0 {
		t.Fatalf("SyncUserProfile: %#x", errCode)
	}
	_, res, err := rmc.NewReader(body).Struct()
	if err != nil {
		t.Fatal(err)
	}
	if pid, _ := res.U64(); pid != 77 {
		t.Fatalf("sync pid %d", pid)
	}
	if name, _ := res.String(); name != "Maker2" {
		t.Fatalf("sync name %q", name)
	}
}

// readUserInfo walks UserInfo v3 field by field (kinnay's datastore_smm2
// UserInfo.load) and insists the structure is consumed exactly.
func readUserInfo(t *testing.T, r *rmc.Reader, pid uint64, name string) {
	t.Helper()
	ver, u, err := r.Struct()
	if err != nil || ver != 3 {
		t.Fatalf("UserInfo header: v%d %v", ver, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := u.U64()
	must(err)
	if got != pid {
		t.Fatalf("pid %d", got)
	}
	code, err := u.String()
	must(err)
	if len(code) != 9 {
		t.Fatalf("maker code %q", code)
	}
	n, err := u.String()
	must(err)
	if n != name {
		t.Fatalf("name %q", n)
	}
	_, _, err = u.Struct()
	must(err) // clothes
	_, err = u.QBuffer()
	must(err)
	_, err = u.String()
	must(err) // country
	_, err = u.U8()
	must(err)
	_, err = u.U64()
	must(err)
	for range 3 {
		_, err = u.Bool()
		must(err)
	}
	u8u32 := func() {
		t.Helper()
		c, err := u.ListHeader()
		must(err)
		for range c {
			_, err = u.U8()
			must(err)
			_, err = u.U32()
			must(err)
		}
	}
	for range 5 {
		u8u32() // play, maker, endless, multiplayer, unk7
	}
	badges, err := u.ListHeader()
	must(err)
	for range badges {
		_, _, err = u.Struct()
		must(err)
	}
	u8u32()
	u8u32()
	_, err = u.Bool()
	must(err)
	_, err = u.U64()
	must(err)
	_, err = u.Bool()
	must(err)
	_, _, err = u.Struct()
	must(err) // UnknownStruct3
	_, err = u.String()
	must(err)
	u8u32()
	_, err = u.Bool()
	must(err)
	if u.Remaining() != 0 {
		t.Fatalf("UserInfo has %d unread bytes", u.Remaining())
	}
}

// TestSMM2CourseWorldListsAreValidEmpties: the hub's lists answer empty
// envelopes, not NotImplemented.
func TestSMM2CourseWorldListsAreValidEmpties(t *testing.T) {
	env := smm2Env()
	for _, m := range []uint32{0x49, 0x4A, 0x47, 0x54, 0x6C, 0x75, 0x9A, 0x3B} {
		if _, _, errCode := decodeResponse(Dispatch(env, conn(1), request(ProtoDataStore, m, nil))); errCode != 0 {
			t.Errorf("method %#x: %#x", m, errCode)
		}
	}
	_, body, _ := decodeResponse(Dispatch(env, conn(1), request(ProtoDataStore, 0x49, nil)))
	if hex.EncodeToString(body) != "0000000001" {
		t.Fatalf("SearchCoursesLatest = %x, want an empty list and true", body)
	}
}

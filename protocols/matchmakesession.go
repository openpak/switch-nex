// MatchmakeSession (and its Gathering base) — the one structure the
// matchmaking protocols carry. Every Switch title runs NEX >= 4.0.0, so the
// field set is fixed: no revision conditionals. Field order follows the
// public NEX type definition (provenance: Pretendo nex-protocols-go
// match-making/types, AGPL-3.0, facts only), verified against the Switch
// byte-stream profile (u64 PIDs, structure headers on Gathering and on the
// embedded MatchmakeParam).
package protocols

import (
	"sort"

	"github.com/openpak/switch-nex/rmc"
)

// Session is one MatchmakeSession. Parsed and re-serialized symmetrically, so
// echoing a stored session reproduces the client's own bytes plus our edits.
type Session struct {
	GID                 uint32
	OwnerPID            uint64
	HostPID             uint64
	MinParticipants     uint16
	MaxParticipants     uint16
	ParticipationPolicy uint32
	PolicyArgument      uint32
	Flags               uint32
	State               uint32
	Description         string

	GameMode              uint32
	Attributes            []uint32
	OpenParticipation     bool
	MatchmakeSystemType   uint32
	ApplicationBuffer     []byte
	ParticipationCount    uint32
	ProgressScore         uint8
	SessionKey            []byte
	Option0               uint32
	MatchmakeParam        map[string]rmc.Variant
	StartedTime           uint64
	UserPassword          string
	ReferGID              uint32
	UserPasswordEnabled   bool
	SystemPasswordEnabled bool
	CodeWord              string
}

// readSession parses one MatchmakeSession (header + all NEX4 fields).
func readSession(r *rmc.Reader) (*Session, error) {
	s := &Session{}
	if _, _, err := r.StructHeader(); err != nil {
		return nil, err
	}
	// Gathering base: its own header, then ten fixed fields.
	if _, _, err := r.StructHeader(); err != nil {
		return nil, err
	}
	var err error
	if s.GID, err = r.U32(); err != nil {
		return nil, err
	}
	if s.OwnerPID, err = r.U64(); err != nil {
		return nil, err
	}
	if s.HostPID, err = r.U64(); err != nil {
		return nil, err
	}
	if s.MinParticipants, err = r.U16(); err != nil {
		return nil, err
	}
	if s.MaxParticipants, err = r.U16(); err != nil {
		return nil, err
	}
	if s.ParticipationPolicy, err = r.U32(); err != nil {
		return nil, err
	}
	if s.PolicyArgument, err = r.U32(); err != nil {
		return nil, err
	}
	if s.Flags, err = r.U32(); err != nil {
		return nil, err
	}
	if s.State, err = r.U32(); err != nil {
		return nil, err
	}
	if s.Description, err = r.String(); err != nil {
		return nil, err
	}

	// MatchmakeSession fields, all present at NEX 4.0.0.
	if s.GameMode, err = r.U32(); err != nil {
		return nil, err
	}
	n, err := r.ListHeader()
	if err != nil {
		return nil, err
	}
	s.Attributes = make([]uint32, 0, min(int(n), 64))
	for i := uint32(0); i < n; i++ {
		var a uint32
		if a, err = r.U32(); err != nil {
			return nil, err
		}
		s.Attributes = append(s.Attributes, a)
	}
	if s.OpenParticipation, err = r.Bool(); err != nil {
		return nil, err
	}
	if s.MatchmakeSystemType, err = r.U32(); err != nil {
		return nil, err
	}
	if s.ApplicationBuffer, err = r.Buffer(); err != nil {
		return nil, err
	}
	if s.ParticipationCount, err = r.U32(); err != nil {
		return nil, err
	}
	if s.ProgressScore, err = r.U8(); err != nil {
		return nil, err
	}
	if s.SessionKey, err = r.Buffer(); err != nil {
		return nil, err
	}
	if s.Option0, err = r.U32(); err != nil {
		return nil, err
	}
	if s.MatchmakeParam, err = readStringVariantMap(r); err != nil {
		return nil, err
	}
	if s.StartedTime, err = r.U64(); err != nil {
		return nil, err
	}
	if s.UserPassword, err = r.String(); err != nil {
		return nil, err
	}
	if s.ReferGID, err = r.U32(); err != nil {
		return nil, err
	}
	if s.UserPasswordEnabled, err = r.Bool(); err != nil {
		return nil, err
	}
	if s.SystemPasswordEnabled, err = r.Bool(); err != nil {
		return nil, err
	}
	if s.CodeWord, err = r.String(); err != nil {
		return nil, err
	}
	return s, nil
}

// write serialises the session (header + Gathering + fields).
func (s *Session) write(w *rmc.Writer) {
	w.StructOf(1, func(w *rmc.Writer) {
		// Gathering base: its own framed structure.
		w.StructOf(1, func(w *rmc.Writer) {
			w.U32(s.GID)
			w.U64(s.OwnerPID)
			w.U64(s.HostPID)
			w.U16(s.MinParticipants)
			w.U16(s.MaxParticipants)
			w.U32(s.ParticipationPolicy)
			w.U32(s.PolicyArgument)
			w.U32(s.Flags)
			w.U32(s.State)
			w.String(s.Description)
		})
		w.U32(s.GameMode)
		w.List(len(s.Attributes))
		for _, a := range s.Attributes {
			w.U32(a)
		}
		w.Bool(s.OpenParticipation)
		w.U32(s.MatchmakeSystemType)
		w.Buffer(s.ApplicationBuffer)
		w.U32(s.ParticipationCount)
		w.U8(s.ProgressScore)
		w.Buffer(s.SessionKey)
		w.U32(s.Option0)
		writeStringVariantMap(w, s.MatchmakeParam)
		w.U64(s.StartedTime)
		w.String(s.UserPassword)
		w.U32(s.ReferGID)
		w.Bool(s.UserPasswordEnabled)
		w.Bool(s.SystemPasswordEnabled)
		w.String(s.CodeWord)
	})
}

// bytes renders the session as a body fragment.
func (s *Session) bytes() []byte {
	w := rmc.NewWriter()
	s.write(w)
	return w.Bytes()
}

func readStringVariantMap(r *rmc.Reader) (map[string]rmc.Variant, error) {
	n, err := r.ListHeader()
	if err != nil {
		return nil, err
	}
	m := make(map[string]rmc.Variant, min(int(n), 64))
	for i := uint32(0); i < n; i++ {
		k, err := r.String()
		if err != nil {
			return nil, err
		}
		v, err := r.ReadVariant()
		if err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, nil
}

// writeStringVariantMap writes the matchmake parameters in ascending key order.
// Go ranges a map in a randomised order, so without the sort a session echoed
// back to a console comes out in a different order every call — and re-encoding
// a session we just parsed does not reproduce its own bytes.
func writeStringVariantMap(w *rmc.Writer, m map[string]rmc.Variant) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.List(len(keys))
	for _, k := range keys {
		w.String(k)
		w.WriteVariant(m[k])
	}
}

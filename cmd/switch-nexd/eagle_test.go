package main

import (
	"fmt"
	"testing"

	"github.com/openpak/switch-nex/eagle"
	"github.com/openpak/switch-nex/rmc"
)

func TestSMB35EagleAutoMatchAndRoundReports(t *testing.T) {
	eagleAutoMatchAndRoundReports(t, "super-mario-bros-35", "wss://g21f12900-lp1.s.n.srv.nintendo.net:22163", eagle.SMB35, 35, true)
}

// PAC-MAN 99 runs the same NEX + Eagle matchmaking and handoff on its own
// registry entry; it links no MatchmakeReferee, so it has no rounds.
func TestPacMan99EagleAutoMatchHandoff(t *testing.T) {
	eagleAutoMatchAndRoundReports(t, "pac-man-99", "wss://g2e471600-lp1.s.n.srv.nintendo.net:23143", eagle.PacMan99, 99, false)
}

func eagleAutoMatchAndRoundReports(t *testing.T, name, base string, profile eagle.Profile, players uint16, referee bool) {
	authAddr, secureAddr, svc := start(t, name)
	relay, err := eagle.NewServerProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	svc.env.EagleTicket = func(gid uint32, pid uint64) (string, string, error) {
		token, err := relay.Ticket(gid, pid)
		return fmt.Sprintf("%s/%d", base, gid), token, err
	}
	svc.env.EagleLeave, svc.env.EagleClose = relay.Leave, relay.CloseRoom
	key := titleAccessKey(t, name)
	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")

	holder := rmc.NewReader(sessionHolderBytes(players, 70010000, 0))
	holder.String()
	holder.U32()
	inner, err := holder.Buffer()
	if err != nil {
		t.Fatal(err)
	}
	param := rmc.NewWriter()
	param.StructOf(0, func(w *rmc.Writer) {
		w.Raw(inner)
		w.EmptyList() // criteria: both clients search for the same session
	})
	resp := host.call(ProtoMatchmakeExt, 0x28, param.Bytes())
	if resp.IsError {
		t.Fatalf("host auto-match: %#x", resp.ErrorCode)
	}
	gid := le32(resp.Body[5:])
	count, _, err := readJoinedSession(resp.Body)
	if err != nil || count != 1 {
		t.Fatalf("host session count=%d err=%v", count, err)
	}
	resp = joiner.call(ProtoMatchmakeExt, 0x28, param.Bytes())
	if resp.IsError || le32(resp.Body[5:]) != gid {
		t.Fatal("joiner did not enter the host's gathering")
	}
	count, _, err = readJoinedSession(resp.Body)
	if err != nil || count != 2 {
		t.Fatalf("joined session count=%d err=%v", count, err)
	}
	hostMap := eagleHandoffFromPushes(t, host.pushes, gid)
	joinerMap := eagleHandoffFromPushes(t, joiner.pushes, gid)
	if hostMap["url"] != fmt.Sprintf("%s/%d", base, gid) || joinerMap["url"] != hostMap["url"] {
		t.Fatal("participants were directed to different or incorrect relay rooms")
	}
	if hostMap["token"] == "" || joinerMap["token"] == "" || hostMap["token"] == joinerMap["token"] {
		t.Fatal("relay tickets are missing or not specific to each player")
	}

	if !referee {
		if resp := host.call(0x78, 1, nil); !resp.IsError {
			t.Fatal("a title without MatchmakeReferee answered StartRound")
		}
		return
	}
	// Eagle's referee signals the round ID in Param1 to every participant,
	// including the initiator, then answers GetStartRoundParam with its body.
	round := rmc.NewWriter()
	round.StructOf(0, func(w *rmc.Writer) {
		w.U32(0)
		w.U32(gid)
		w.List(2)
		w.U64(host.pid)
		w.U64(joiner.pid)
		w.U8(0) // Eagle report-summary mode
		w.U32(0)
	})
	resp = host.call(0x78, 1, round.Bytes())
	if resp.IsError {
		t.Fatalf("StartRound: %#x", resp.ErrorCode)
	}
	roundID, err := rmc.NewReader(resp.Body).U64()
	if err != nil || roundID == 0 {
		t.Fatal("no referee round ID")
	}
	id := rmc.NewWriter()
	id.U64(roundID)
	resp = joiner.call(0x78, 2, id.Bytes())
	if resp.IsError || string(resp.Body) != string(round.Bytes()) {
		t.Fatal("GetStartRoundParam did not echo the Eagle start parameters")
	}
	for _, pushes := range [][]*rmc.RMCMessage{host.pushes, joiner.pushes} {
		found := false
		for _, push := range pushes {
			if push.Protocol != ProtoNotification {
				continue
			}
			_, r, err := rmc.NewReader(push.Body).Struct()
			if err != nil {
				t.Fatal(err)
			}
			r.U64()
			typ, _ := r.U32()
			if typ == 116000 {
				got, _ := r.U64()
				if got != roundID {
					t.Fatalf("round notification names %d, want %d", got, roundID)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("a participant did not receive the round-start notification")
		}
	}

	// Eliminated players report before the winner. Neither a partial report
	// nor a final report may remove the shared round before the winner reports.
	// Exercise both finishing orders and retries over real NEX connections.
	for order, players := range [][]*client{{joiner, host}, {host, joiner}} {
		if order > 0 {
			resp = host.call(0x78, 1, round.Bytes())
			if resp.IsError {
				t.Fatalf("next StartRound: %#x", resp.ErrorCode)
			}
			roundID, err = rmc.NewReader(resp.Body).U64()
			if err != nil || roundID == 0 {
				t.Fatal("no next referee round ID")
			}
			id = rmc.NewWriter()
			id.U64(roundID)
		}
		end := rmc.NewWriter()
		end.StructOf(0, func(w *rmc.Writer) {
			w.U64(roundID)
			w.EmptyList() // personal round results
		})
		for _, report := range []struct {
			player *client
			method uint32
		}{
			{players[0], 4}, // partial report
			{players[0], 3}, // eliminated player's final report
			{players[1], 3}, // winner's final report
			{players[0], 3}, // retry
			{players[1], 5}, // finish without a report
		} {
			body := end.Bytes()
			if report.method == 5 {
				body = id.Bytes()
			}
			resp = report.player.call(0x78, report.method, body)
			if resp.IsError {
				t.Fatalf("finishing order %d, report method %d: %#x", order, report.method, resp.ErrorCode)
			}
			resp = players[1].call(0x78, 2, id.Bytes())
			if resp.IsError || string(resp.Body) != string(round.Bytes()) {
				t.Fatalf("finishing order %d: report removed the shared round", order)
			}
		}
	}
	missing := rmc.NewWriter()
	missing.StructOf(0, func(w *rmc.Writer) {
		w.U64(roundID + 1)
		w.EmptyList()
	})
	resp = host.call(0x78, 3, missing.Bytes())
	if !resp.IsError || resp.ErrorCode != rmc.ResultRefereeRoundGone {
		t.Fatal("an unknown round must still return RoundNotFound")
	}
}

func eagleHandoffFromPushes(t *testing.T, pushes []*rmc.RMCMessage, gid uint32) map[string]string {
	t.Helper()
	for _, push := range pushes {
		if push.Protocol != ProtoNotification {
			continue
		}
		version, r, err := rmc.NewReader(push.Body).Struct()
		if err != nil {
			t.Fatal(err)
		}
		source, _ := r.U64()
		typ, _ := r.U32()
		if typ != 200000 {
			continue
		}
		room, _ := r.U64()
		if version != 1 || source != 257049437023956657 || room != uint64(gid) {
			t.Fatal("Eagle handoff has the wrong revision, source principal or room")
		}
		r.U64()
		r.String()
		r.U64()
		n, err := r.ListHeader()
		if err != nil || n != 2 {
			t.Fatal("handoff map is not a bare count followed by two pairs")
		}
		values := map[string]string{}
		for range n {
			key, err := r.String()
			if err != nil {
				t.Fatal(err)
			}
			value, err := r.ReadVariant()
			if err != nil || value.TypeID != 4 {
				t.Fatalf("handoff value: %v", err)
			}
			values[key] = value.Str
		}
		if r.Remaining() != 0 {
			t.Fatal("handoff has trailing bytes")
		}
		return values
	}
	t.Fatal("participant did not receive an Eagle handoff")
	return nil
}

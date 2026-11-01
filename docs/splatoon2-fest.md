# Splatoon 2 Splatfest on Ranking (0x70)

Source of the layouts: the public NintendoClients wiki page
[Ranking Protocol (Splatoon 2)](https://github.com/kinnay/NintendoClients/wiki/Ranking-Protocol-(Splatoon-2))
(mirrored at nintendo-wiki.pretendo.network). Nothing here is from a capture.

| Method | Name | Request | Response | Status |
| --- | --- | --- | --- | --- |
| 0x12 (18) | GetFestivalResult | festivalId u32 | FestivalResult | implemented |
| 0x13 (19) | UploadFestivalVote | struct{festivalId u32, themeId u8} | none | implemented |
| 0x14 (20) | UploadFestivalScore | struct{uniqueId u64, region u8, festivalId u32, teamId u8, score u32, battleGatheringId u32, battleResult u32, applicationBuffer qBuffer} | none | implemented |
| 0x15 (21) | DeleteFestival | festivalId u32 | none | implemented |
| 0x19 (25) | GetEventMatchResult | not public | not public | **gap**, NotImplemented |
| 0x18 / 0x1A | UploadEventMatchResult / AcquireEventMatchRight | not public | not public | gap |
| 0x10 / 0x11 | UploadLeaguePoint / GetLeagueResult | public, not done | | still answered with the generic 0x10/0x11 handlers; out of scope |
| 0x16, 0x17 | UploadXPower / GetXPowerRanking | not public | | gap |

Note the task brief called 0x70.19 "festival rank": the public page names it **UploadFestivalVote**
(a write with no response). The earlier Wii U stub answered an empty list for it; here it records the
vote and answers an empty success.

FestivalResult is written as structure revision 0 (festivalId, numParticipants, teamParticipants
list<u32>, teamScores list<u32>, updatedTime DateTime). Revision 1 of the structure adds a u8 and makes
the scores u64; clients read by the header version, so revision 0 is a valid answer. Three team slots
are always written (a two-team fest leaves the third at zero). Votes are one per player (last wins);
scores add up. State is in memory (`protocols/splatoon2_fest.go`), reset on restart, capped at 64 fests.

All of this is gated on the title name `splatoon-2`: the same method ids mean other things on other
titles (0x12 GetCompetitionInfo, 0x13 SubmitTime).

## What a capture must show (the gaps)

1. 0x19 GetEventMatchResult: request body (expect an event id and maybe a tag/region) and what the
   game does with an empty list vs NotImplemented. Capture the call from the event-match (Squad
   Battle / "Splatfest Tricolor"?) screen: bytes of the request and, from Nintendo's reply if ever
   available, the response. Without a reply a decoded client-side parse (Ghidra) is the only source.
2. Whether the Switch build wants FestivalResult revision 1 (unnamed u8, u64 scores), and what the u8 is.
3. How many teams a fest carries (two, or three for Tricolor), which fixes the list length and the
   valid `themeId`/`teamId` range (currently 0..2).
4. Whether `numParticipants` is the sum of team participants (assumed) and whether `teamScores` is
   points or a battle count.
5. Whether the festival id in these calls equals `FestivalId` in `fesdata/Festival.byaml`.
6. Which of 0x12/0x13/0x14 the lobby calls at fest start, and whether a missing GetFestivalResult
   blocks the fest screen.

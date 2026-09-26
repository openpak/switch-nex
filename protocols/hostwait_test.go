package protocols

import "testing"

// Regression check: a host's post-ReplaceURL stations must satisfy the wait, or
// GetSessionURLs burns its whole 2.5s on every join. The strings are one real
// Kirby host's stations, copied from the 2026-09-26 journal.
func TestKirbyHostStationsSatisfyReplaceURLWait(t *testing.T) {
	stations := []string{
		"prudp:/address=10.87.0.8;port=12068;CID=447795488;PID=12297829382472990722;Pl=3;RVCID=4;Tpt=2;natf=34;natm=1;pmp=0;sid=30;upnp=0;PID=12297829382472990722",
		"prudp:/address=37.228.205.45;port=12110;Pl=3;Tpt=2;natf=0;natm=0;pmp=0;sid=30;upnp=0;port=12110;PID=12297829382472990722;RVCID=4;type=11;Pa=37.228.205.45",
	}
	if !hasValidReplaceURL(stations) {
		t.Fatalf("host stations rejected: GetSessionURLs burns its full 2.5s wait")
	}
}

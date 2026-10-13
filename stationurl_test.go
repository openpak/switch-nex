package switchnex

import "testing"

func TestStationURLReplacesFieldsWithoutDuplicates(t *testing.T) {
	u := Parse("prudp:/address=192.168.1.20;port=50000;type=1;port=50001")
	u.SetInt("port", 52000)
	u.SetInt("type", 3)
	u.Set("type", "11")
	u.SetInt("type", 3)
	want := "prudp:/address=192.168.1.20;port=52000;type=3"
	if got := u.String(); got != want {
		t.Fatalf("station dictionary emitted duplicate fields: %q, want %q", got, want)
	}
	copy := u.Copy()
	copy.Set("port", "53000")
	if got := copy.String(); got != "prudp:/address=192.168.1.20;port=53000;type=3" {
		t.Fatalf("copy/update: %q", got)
	}
	if u.String() != want {
		t.Fatal("copy changed the original station")
	}
	copy.Remove("type")
	copy.SetInt("type", 11)
	if got := copy.String(); got != "prudp:/address=192.168.1.20;port=53000;type=11" {
		t.Fatalf("remove/reinsert: %q", got)
	}
}

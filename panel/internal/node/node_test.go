package node

import "testing"

func TestParseLoadAvg(t *testing.T) {
	got, err := parseLoadAvg("0.42 0.31 0.25 1/234 5678\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0.42 {
		t.Fatalf("want 0.42, got %v", got)
	}
}

func TestParseLoadAvgBad(t *testing.T) {
	if _, err := parseLoadAvg(""); err == nil {
		t.Fatal("expected error on empty input")
	}
}

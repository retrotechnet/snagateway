package sna

import "testing"

func TestBuildSSCPLUSegments_Small(t *testing.T) {
	pius := BuildSSCPLUSegments(2, 7, []byte("HELLO"))
	if len(pius) != 1 {
		t.Fatalf("small data should be one PIU, got %d", len(pius))
	}
	p, err := ParsePIU(pius[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.TH.MPF != MPFWhole {
		t.Errorf("MPF = %d, want MPFWhole", p.TH.MPF)
	}
	if p.TH.DAF != 2 || p.TH.OAF != 0 {
		t.Errorf("DAF/OAF = %d/%d, want 2/0", p.TH.DAF, p.TH.OAF)
	}
	if string(p.RU) != "HELLO" {
		t.Errorf("RU = %q, want HELLO", p.RU)
	}
}

func TestBuildSSCPLUSegments_Large(t *testing.T) {
	data := make([]byte, 3500) // > 2 BTUs -> 3 segments
	for i := range data {
		data[i] = byte('A' + i%26)
	}
	pius := BuildSSCPLUSegments(3, 9, data)
	if len(pius) < 3 {
		t.Fatalf("3500 bytes should segment into >=3 PIUs, got %d", len(pius))
	}

	// Only the first segment carries the RH, so parse just the TH (first 6 bytes)
	// for header checks and take everything after it as the raw segment payload.
	var biu []byte
	for i, raw := range pius {
		th, err := ParseTH(raw[:6])
		if err != nil {
			t.Fatalf("segment %d TH parse: %v", i, err)
		}
		if th.DAF != 3 || th.SNF != 9 {
			t.Errorf("segment %d DAF/SNF = %d/%d, want 3/9 (shared)", i, th.DAF, th.SNF)
		}
		wantMPF := byte(MPFMiddle)
		switch {
		case i == 0:
			wantMPF = MPFFirst
		case i == len(pius)-1:
			wantMPF = MPFLast
		}
		if th.MPF != wantMPF {
			t.Errorf("segment %d MPF = %d, want %d", i, th.MPF, wantMPF)
		}
		biu = append(biu, raw[6:]...) // payload after the 6-byte TH
	}
	// Reassembled BIU = 3-byte RH + the original data.
	if len(biu) < 3 || string(biu[3:]) != string(data) {
		t.Errorf("reassembled data mismatch: got %d payload bytes, want %d", len(biu)-3, len(data))
	}
}

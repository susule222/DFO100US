package protocol

import (
	"encoding/binary"
	"testing"
)

func TestEventProtocolWireLayouts(t *testing.T) {
	// 1. NOTI 108 Part 2 with params
	evBytes, err := ActiveEventsDetailedWithParams(
		[]ActiveEventEntry{{EventID: 607, HasDetail: false}},
		[]EventInitParam{{EventID: 607, Params: [12]uint32{1}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// 2 (count) + 3 (entry without detail) + 1 (paramCount=1) + 2 (id) + 48 (12*u32) = 56
	if len(evBytes) != 56 {
		t.Fatalf("NOTI 108 with 1 param len = %d, want 56", len(evBytes))
	}
	if evBytes[5] != 1 || binary.LittleEndian.Uint16(evBytes[6:8]) != 607 || binary.LittleEndian.Uint32(evBytes[8:12]) != 1 {
		t.Fatalf("unexpected NOTI 108 Part 2 bytes: %x", evBytes[5:16])
	}

	// 2. CMD 680 / 681 decoder
	cmd, err := DecodeEventCommand([]byte{0x08, 0x02, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00})
	if err != nil || cmd.EventID != 520 || len(cmd.Args) != 1 || cmd.Args[0] != 1 {
		t.Fatalf("DecodeEventCommand = %+v err=%v", cmd, err)
	}

	// 3. ACK 680 success = 45 bytes (1 + 4 + 40)
	ack := EventActionSuccess(367, [10]uint32{2, 50061692, 3, 50061703, 1, 0, 0, 0, 0, 1})
	if len(ack) != 45 || ack[0] != 1 || binary.LittleEndian.Uint32(ack[1:5]) != 367 || binary.LittleEndian.Uint32(ack[41:45]) != 1 {
		t.Fatalf("unexpected ACK 680 payload len=%d hex=%x", len(ack), ack)
	}

	// 4. Individual event NOTI exact byte lengths
	if n := Ev520ReverseHotTimeStatus(600, true, false, 1); len(n) != 12 || binary.LittleEndian.Uint32(n[0:4]) != 600 || n[4] != 1 || n[5] != 0 {
		t.Fatalf("unexpected NOTI 2222: len=%d hex=%x", len(n), n)
	}
	if n := Ev512DungeonMiningStatus(10, [4]uint32{1, 0, 0, 0}); len(n) != 20 || binary.LittleEndian.Uint32(n[0:4]) != 10 || binary.LittleEndian.Uint32(n[4:8]) != 1 {
		t.Fatalf("unexpected NOTI 2155: len=%d hex=%x", len(n), n)
	}
	if n := Ev388Missions([]Ev388MissionRow{{Index: 0, Completed: true, Progress: 3}, {Index: 2, Completed: true, Progress: 1800}}); len(n) != 9 {
		t.Fatalf("unexpected NOTI 1784 len=%d, want 9", len(n))
	}
	if n := Ev515EverydayConnectStatus(true, true, 5, 0x1e, 0x20); len(n) != 11 || n[0] != 1 || n[1] != 1 || n[2] != 5 {
		t.Fatalf("unexpected NOTI 2215: len=%d hex=%x", len(n), n)
	}
	if n := Ev619DailyCheckStatus(3600, 0x3, 0, 5, 0x1); len(n) != 20 || binary.LittleEndian.Uint32(n[4:8]) != 3600 || n[8] != 0 || binary.LittleEndian.Uint32(n[12:16]) != 0x3 || n[16] != 5 || n[17] != 1 {
		t.Fatalf("unexpected NOTI 2161 (unclaimed): len=%d hex=%x", len(n), n)
	}
	if n := Ev619DailyCheckStatus(3600, 0x7, 1, 3, 0x0); len(n) != 20 || n[8] != 1 || binary.LittleEndian.Uint32(n[12:16]) != 0x7 {
		t.Fatalf("unexpected NOTI 2161 (claimed): len=%d hex=%x", len(n), n)
	}
	if n := Ev614MelodyStatus([4]byte{3, 1, 200, 20}, [4]byte{1, 0, 0, 0}, 4, 0x1); len(n) != 10 || n[8] != 4 || n[9] != 1 {
		t.Fatalf("unexpected NOTI 2134: len=%d hex=%x", len(n), n)
	}
	if n := Ev602RiaCubeLabStatus(Ev602CubeLabState{SavedItemIdx: 2, PickedIndices: []byte{0, 2}, TransferredMask: 1, EnergyPoints: 100, CurrentItemIdx: -1}); len(n) != 52 || n[2] != 2 || n[3] != 2 || n[4] != 0 || n[5] != 2 || binary.LittleEndian.Uint32(n[0x1c:0x20]) != 1 || binary.LittleEndian.Uint32(n[0x2c:0x30]) != 100 {
		t.Fatalf("unexpected NOTI 1949: len=%d hex=%x", len(n), n)
	}
}

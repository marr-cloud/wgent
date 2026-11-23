package health

import (
	"strings"
	"testing"
	"time"
)

const dumpTwoPeers = "PRIV\tPUB\t51820\toff\n" +
	"peerA=\t(none)\t203.0.113.5:51820\t10.0.0.2/32\t1795000000\t1234\t5678\t25\n" +
	"peerB=\t(none)\t(none)\t10.0.1.0/24\t0\t0\t0\toff\n"

func checkTwoPeers(t *testing.T, peers []Peer) {
	t.Helper()
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}
	a, b := peers[0], peers[1]
	if a.PublicKey != "peerA=" || a.Endpoint != "203.0.113.5:51820" || a.RxBytes != 1234 || a.TxBytes != 5678 {
		t.Errorf("peer A = %+v", a)
	}
	if !a.LastHandshake.Equal(time.Unix(1795000000, 0)) {
		t.Errorf("peer A handshake = %v", a.LastHandshake)
	}
	if b.Endpoint != "" || !b.LastHandshake.IsZero() {
		t.Errorf("peer B = %+v", b)
	}
}

func TestParseDumpTwoPeers(t *testing.T) {
	peers, err := ParseDump(dumpTwoPeers)
	if err != nil {
		t.Fatal(err)
	}
	checkTwoPeers(t, peers)
}

func TestParseDumpCRLF(t *testing.T) {
	peers, err := ParseDump(strings.ReplaceAll(dumpTwoPeers, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	checkTwoPeers(t, peers)
}

func TestParseDumpBadLine(t *testing.T) {
	if _, err := ParseDump("PRIV\tPUB\t51820\toff\npeer\ta\tb\tc\td\n"); err == nil {
		t.Fatal("expected error for 5-field line")
	}
}

func TestParseDumpNonNumeric(t *testing.T) {
	if _, err := ParseDump("PRIV\tPUB\t51820\toff\np\t(none)\t(none)\t0.0.0.0/0\tx\t0\t0\toff\n"); err == nil {
		t.Fatal("expected error for non-numeric handshake")
	}
	if _, err := ParseDump("PRIV\tPUB\t51820\toff\np\t(none)\t(none)\t0.0.0.0/0\t0\tx\t0\toff\n"); err == nil {
		t.Fatal("expected error for non-numeric rx")
	}
}

func TestParseDumpInterfaceOnly(t *testing.T) {
	peers, err := ParseDump("PRIV\tPUB\t51820\toff\n")
	if err != nil || len(peers) != 0 {
		t.Fatalf("peers=%v err=%v", peers, err)
	}
}

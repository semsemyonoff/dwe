//go:build linux || darwin

package bridgeproto

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// unixPair returns the two ends of a connected unix-socket pair in a tmpdir.
func unixPair(t *testing.T) (server, client *net.UnixConn) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		t.Fatalf("ResolveUnixAddr: %v", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	type acceptResult struct {
		conn *net.UnixConn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := ln.AcceptUnix()
		acceptCh <- acceptResult{conn, err}
	}()

	client, err = net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	res := <-acceptCh
	if res.err != nil {
		t.Fatalf("AcceptUnix: %v", res.err)
	}
	t.Cleanup(func() { _ = res.conn.Close() })
	return res.conn, client
}

func TestPeerUID_SameProcess(t *testing.T) {
	server, _ := unixPair(t)
	uid, err := PeerUID(server)
	if err != nil {
		t.Fatalf("PeerUID: %v", err)
	}
	if int64(uid) != int64(os.Getuid()) {
		t.Errorf("peer uid = %d, want %d", uid, os.Getuid())
	}
}

func TestPeerIsSameUser_SameProcess(t *testing.T) {
	server, client := unixPair(t)
	for name, conn := range map[string]*net.UnixConn{"server end": server, "client end": client} {
		ok, err := PeerIsSameUser(conn)
		if err != nil {
			t.Fatalf("%s: PeerIsSameUser: %v", name, err)
		}
		if !ok {
			t.Errorf("%s: PeerIsSameUser = false, want true for same-process pair", name)
		}
	}
}

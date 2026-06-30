package delivery

import "testing"

func TestBuildCleanupCommandsRemovesConnectionFromAllIndexes(t *testing.T) {
	cmds := buildCleanupCommands("ws-server-01", map[string]int64{"conn-1": 1001, "conn-2": 1002})
	if len(cmds) != 7 {
		t.Fatalf("expected 7 cleanup commands, got %d", len(cmds))
	}
	if cmds[0].key != "connection:conn-1:server" || cmds[1].key != "connection:conn-1:user" {
		t.Fatalf("unexpected connection cleanup commands: %#v", cmds[:2])
	}
	if cmds[2].key != "online:user:1001:connections" || cmds[2].member != "conn-1" {
		t.Fatalf("unexpected user connection cleanup command: %#v", cmds[2])
	}
	if cmds[len(cmds)-1].key != "server:ws-server-01:connections" {
		t.Fatalf("expected server connection set cleanup, got %#v", cmds[len(cmds)-1])
	}
}

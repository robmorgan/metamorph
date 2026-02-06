package daemon

import "testing"

func TestDaemonPlaceholder(t *testing.T) {
	d := Daemon{PIDFile: "/tmp/test.pid"}
	if d.PIDFile != "/tmp/test.pid" {
		t.Fatal("unexpected PIDFile")
	}
}

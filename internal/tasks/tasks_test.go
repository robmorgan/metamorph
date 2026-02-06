package tasks

import "testing"

func TestLockerPlaceholder(t *testing.T) {
	l := Locker{LockDir: "current_tasks"}
	if l.LockDir != "current_tasks" {
		t.Fatal("unexpected LockDir")
	}
}

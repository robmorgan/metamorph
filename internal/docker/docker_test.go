package docker

import "testing"

func TestManagerPlaceholder(t *testing.T) {
	m := Manager{Image: "ubuntu:22.04"}
	if m.Image != "ubuntu:22.04" {
		t.Fatal("unexpected Image")
	}
}

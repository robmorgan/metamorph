package gitops

import "testing"

func TestRepoPlaceholder(t *testing.T) {
	r := Repo{UpstreamDir: ".metamorph/upstream.git"}
	if r.UpstreamDir != ".metamorph/upstream.git" {
		t.Fatal("unexpected UpstreamDir")
	}
}

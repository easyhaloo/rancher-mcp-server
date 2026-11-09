package kubernetes

import "testing"

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"id", []string{"id"}},
		{"id; hostname", []string{"id;", "hostname"}},
		{"sh -c 'ls -la /app'", []string{"sh", "-c", "ls -la /app"}},
		{`sh -c "echo a b"`, []string{"sh", "-c", "echo a b"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{`echo "it's ok"`, []string{"echo", "it's ok"}},
		{`echo a\ b`, []string{"echo", "a b"}},
		{"", nil},
	}
	for _, c := range cases {
		got := splitCommand(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitCommand(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitCommand(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestSplitCommandKeepsEmptyQuotedArg(t *testing.T) {
	got := splitCommand(`sh -c 'echo ""'`)
	if len(got) != 3 || got[2] != `echo ""` {
		t.Fatalf("got %v", got)
	}
}

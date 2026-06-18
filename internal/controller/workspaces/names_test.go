package workspaces

import "testing"

func TestBoundedName(t *testing.T) {
	// The exact name that broke Sveltos ClusterSummary creation in the field:
	// "wsprereq-" + "default-child-adopted-1" + "-" + "test-prereq-fake-operator-0.1.0"
	regression := prerequisiteServiceSetName("default-child-adopted-1", "test-prereq-fake-operator-0.1.0")
	if len(regression) > maxServiceSetNameLen {
		t.Fatalf("prerequisiteServiceSetName too long: %d chars (%q)", len(regression), regression)
	}

	cases := []struct {
		name string
		in   string
	}{
		{"short unchanged", "wsd-cd-ws"},
		{"exactly 63 unchanged", "wsprereq-default-child-adopted-1-test-prereq-fake-operator-0.1"}, // 63
		{"64 gets bounded", "wsprereq-default-child-adopted-1-test-prereq-fake-operator-0.1.0"},    // 64
		{"very long gets bounded", "wsd-default-child-adopted-1-" + repeat("a", 80)},
	}

	for _, c := range cases {
		got := boundedName(c.in)
		if len(got) > maxServiceSetNameLen {
			t.Errorf("%s: boundedName(%q) = %q is %d chars, want <= %d", c.name, c.in, got, len(got), maxServiceSetNameLen)
		}
		if len(c.in) <= maxServiceSetNameLen && got != c.in {
			t.Errorf("%s: boundedName must leave a fitting name untouched, got %q want %q", c.name, got, c.in)
		}
		// Determinism.
		if again := boundedName(c.in); again != got {
			t.Errorf("%s: boundedName not deterministic: %q vs %q", c.name, got, again)
		}
		// DNS-1123 / label boundary: must end alphanumeric.
		last := got[len(got)-1]
		isLower := last >= 'a' && last <= 'z'
		isDigit := last >= '0' && last <= '9'
		if !isLower && !isDigit {
			t.Errorf("%s: boundedName(%q) = %q must end with an alphanumeric", c.name, c.in, got)
		}
	}

	// Distinct long inputs must not collide.
	a := boundedName("wsd-default-child-adopted-1-" + repeat("a", 80))
	b := boundedName("wsd-default-child-adopted-1-" + repeat("b", 80))
	if a == b {
		t.Errorf("distinct long names collided: %q == %q", a, b)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

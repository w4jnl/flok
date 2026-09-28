package agent

import "testing"

func TestPaneRefRoundTrip(t *testing.T) {
	for in, want := range map[string]PaneRef{
		"%12":          {ID: "%12"},
		"local:%12":    {ID: "%12"},
		"beta:%12":     {Host: "beta", ID: "%12"},
		"gpu-2:%0":     {Host: "gpu-2", ID: "%0"},
		"dockerAMS:%3": {Host: "dockerAMS", ID: "%3"}, // an ssh alias keeps its spelling
	} {
		got, err := ParsePaneRef(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v, %v; want %+v", in, got, err, want)
		}
		if s := got.String(); (want.Host == "" && s != want.ID) || (want.Host != "" && s != in) {
			t.Errorf("%q: String() = %q", in, s)
		}
	}
	for _, bad := range []string{"beta:12", "-x:%1", "be.ta:%1", "%1a", "", ":%1", "a:b:%1"} {
		if _, err := ParsePaneRef(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	if r, err := ParsePaneRef("LOCAL:%4"); err != nil || r != (PaneRef{ID: "%4"}) {
		t.Errorf("LOCAL:%%4 names the local server: %+v %v", r, err)
	}
	if (PaneRef{ID: "%3"}).String() != "%3" || (PaneRef{Host: "beta", ID: "%3"}).String() != "beta:%3" {
		t.Fatal("String forms")
	}
}

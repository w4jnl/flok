package state

import "testing"

func TestNamesRoundTrip(t *testing.T) {
	st := New(t.TempDir())
	if n := st.LoadNames(); len(n) != 0 {
		t.Fatalf("fresh store: %v", n)
	}
	if err := st.SetName("%12", "gex"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetName("beta:%3", "api"); err != nil {
		t.Fatal(err)
	}
	if n := st.LoadNames(); n["%12"] != "gex" || n["beta:%3"] != "api" || len(n) != 2 {
		t.Fatalf("names %v", n)
	}
	if err := st.SetName("%12", ""); err != nil {
		t.Fatal(err)
	}
	if n := st.LoadNames(); len(n) != 1 || n["beta:%3"] != "api" {
		t.Fatalf("after forgetting one: %v", n)
	}
	if err := st.SetName("beta:%3", ""); err != nil {
		t.Fatal(err)
	}
	if n := st.LoadNames(); len(n) != 0 {
		t.Fatalf("after forgetting all: %v", n)
	}
}

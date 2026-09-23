package executor

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		`server /data --console-address ":9001"`: {"server", "/data", "--console-address", ":9001"},
		`start --pass 'a b' x\ y`:                {"start", "--pass", "a b", "x y"},
		`  -js   -m 8222 `:                       {"-js", "-m", "8222"},
		`echo "it's"`:                            {"echo", "it's"},
		``:                                       nil,
	}
	for in, want := range cases {
		got, err := SplitArgs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("SplitArgs(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := SplitArgs(`echo "open`); err == nil {
		t.Error("unterminated quote must fail")
	}
}

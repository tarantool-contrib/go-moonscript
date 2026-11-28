package lua

// go-moonscript patch: os.setlocale("") takes the locale from the
// environment, as setlocale(3) does (the results are glibc's, checked with
// lua5.1 in the moonref image run with these variables).

import "testing"

func TestSetLocaleEnv(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{}, "C\tC\tC"},
		{map[string]string{"LANG": "C.UTF-8"}, "C.UTF-8\tC.UTF-8\tC.UTF-8"},
		{map[string]string{"LC_ALL": "xx"}, "nil\tC\tC"},
		{map[string]string{"LANG": "C.UTF-8", "LC_NUMERIC": "C"},
			"LC_CTYPE=C.UTF-8;LC_NUMERIC=C;LC_TIME=C.UTF-8;LC_COLLATE=C.UTF-8;LC_MONETARY=C.UTF-8;LC_MESSAGES=C.UTF-8;LC_PAPER=C.UTF-8;LC_NAME=C.UTF-8;LC_ADDRESS=C.UTF-8;LC_TELEPHONE=C.UTF-8;LC_MEASUREMENT=C.UTF-8;LC_IDENTIFICATION=C.UTF-8\tC\tC.UTF-8"},
		{map[string]string{"LANG": "en_US.UTF-8", "LC_CTYPE": "C"}, "nil\tC\tC"},
	} {
		for _, v := range []string{"LANG", "LC_ALL", "LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE",
			"LC_MONETARY", "LC_MESSAGES", "LC_PAPER", "LC_NAME", "LC_ADDRESS", "LC_TELEPHONE",
			"LC_MEASUREMENT", "LC_IDENTIFICATION"} {
			t.Setenv(v, c.env[v])
		}
		L := NewState()
		if err := L.DoString(`r = table.concat({tostring(os.setlocale("")), os.setlocale(nil, "numeric"), os.setlocale(nil, "ctype")}, "\t")`); err != nil {
			t.Fatal(err)
		}
		if got := L.GetGlobal("r").String(); got != c.want {
			t.Errorf("%v: got %q, want %q", c.env, got, c.want)
		}
		L.Close()
	}
}

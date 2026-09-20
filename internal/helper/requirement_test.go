package helper

import "testing"

func TestRequirement(t *testing.T) {
	const id = "dev.kyleupton.flashit"
	cases := []struct {
		name           string
		id, leaf, team string
		want           string
	}{
		{"dev leaf hash", id, "ad62a0fcdd9da6353321af70a1670c9ae2a5499f", "",
			`identifier "dev.kyleupton.flashit" and certificate leaf = H"AD62A0FCDD9DA6353321AF70A1670C9AE2A5499F"`},
		{"release team", id, "", "AB12CD34EF",
			`identifier "dev.kyleupton.flashit" and anchor apple generic and certificate leaf[subject.OU] = "AB12CD34EF"`},
		{"neither", id, "", "", ""},
		{"both", id, "AD62A0FCDD9DA6353321AF70A1670C9AE2A5499F", "AB12CD34EF", ""},
		{"short hash", id, "AD62A0FC", "", ""},
		{"hash with quote", id, `AD62A0FCDD9DA6353321AF70A1670C9AE2A5499"`, "", ""},
		{"team too short", id, "", "AB12", ""},
		{"team lower case", id, "", "ab12cd34ef", ""},
		{"team with quote", id, "", `AB12CD34E"`, ""},
		{"empty identifier", "", "", "AB12CD34EF", ""},
		{"identifier with quote", `dev.kyleupton" or identifier "x`, "", "AB12CD34EF", ""},
		{"identifier with space", "dev.kyleupton flashit", "", "AB12CD34EF", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Requirement(c.id, c.leaf, c.team)
			if c.want == "" {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

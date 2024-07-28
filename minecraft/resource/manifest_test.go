package resource

import (
	"encoding/json"
	"testing"
)

func TestVersion(t *testing.T) {
	var tests = []string{
		`"1.0.0"`,
		`["1","0","0"]`,
		`[1,0,0]`,
	}

	for _, test := range tests {
		var ver Version
		err := json.Unmarshal([]byte(test), &ver)
		if err != nil {
			t.Fatal(err)
		}
	}
}

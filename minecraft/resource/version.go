package resource

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Version [3]int

func (v *Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

func (v *Version) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("[%d, %d, %d]", v[0], v[1], v[2])), nil
}

func (v *Version) UnmarshalJSON(b []byte) error {
	var val any
	err := json.Unmarshal(b, &val)
	if err != nil {
		return err
	}
	switch val := val.(type) {
	case []any:
		for i, vv := range val[:min(len(val), 3)] {
			r := &v[i]
			switch vv := vv.(type) {
			case float64:
				*r = int(vv)
			case int:
				*r = vv
			case string:
				*r, err = strconv.Atoi(vv)
				if err != nil {
					return err
				}
			}
		}
	case string:
		sp := strings.SplitN(strings.SplitN(val, "-", 2)[0], ".", 3)
		if len(sp) == 3 {
			v[0], err = strconv.Atoi(sp[0])
			if err != nil {
				return err
			}
			v[1], err = strconv.Atoi(sp[1])
			if err != nil {
				return err
			}
			v[2], err = strconv.Atoi(sp[2])
			if err != nil {
				return err
			}
		}
	}
	return nil
}

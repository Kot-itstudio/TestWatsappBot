package commands

import (
	"encoding/json"
	"strconv"
)

func itoa(i int) string {
	return strconv.Itoa(i)
}

func marshalJSONSafe(v interface{}) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{\"error\":\"не удалось сериализовать конфиг\"}"
	}
	return string(raw)
}

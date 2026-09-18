package bootstrap

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func newID(prefix string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		// crypto/rand failure is exceptionally rare and there is no safe value
		// to return to the caller. Include no secret data in the panic.
		panic(fmt.Sprintf("generate %s identifier: %v", prefix, err))
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}

func idString(value interface{}) string {
	switch typed := value.(type) {
	case SpaceID:
		return string(typed)
	case ListID:
		return string(typed)
	case TaskID:
		return string(typed)
	case ProviderID:
		return string(typed)
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

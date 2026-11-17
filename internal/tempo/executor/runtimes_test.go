package executor_test

import (
	"encoding/hex"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/kapetan-io/tiger/assert"
)

func runtimeHash(t *testing.T, name string) string {
	code, err := os.ReadFile("runtimes/" + name)
	assert.NoError(err, "read runtime")
	h := keccak.Sum256(code)
	return hex.EncodeToString(h[:])
}

func TestZoneRuntimesMatchMainnetCodeHashes(t *testing.T) {
	assert.Equal(runtimeHash(t, "t10_zone_portal.bin"), "109c27a83ea58d7b91efcc7bb415a04abef5a52d8fdc5a246c96de965fa47b32", "t10 portal")
	assert.Equal(runtimeHash(t, "t10_zone_verifier.bin"), "f3ab166b6d8637bb2f15ac5037b4546611339fc88e4b03b00b89559f7f2822a2", "t10 verifier")
	assert.Equal(runtimeHash(t, "t10_zone_messenger.bin"), "1ea2e80521c58fd7966e1fd370f84ac297261c7197f097ffc879de1890e0a1a0", "t10 messenger")
	assert.Equal(runtimeHash(t, "t13_zone_portal.bin"), "bb0c0e14d5c2e15ca4aa1aaeb8d5c7bfdcf35fdadac5380ff93d38f962e4c41e", "t13 portal")
	assert.Equal(runtimeHash(t, "t13_zone_verifier.bin"), "cf7b19d3c186e4fd235c94907d10bba5c1ce21d6e815676b00aaf298b456de14", "t13 verifier")
	assert.Equal(runtimeHash(t, "t13_zone_messenger.bin"), "526c5bb045b6229e5a693d70feee066cc3848b5f91c62949ced54818045214ee", "t13 messenger")
}

package scorer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractJSON_MarkdownFence(t *testing.T) {
	in := "```json\n{\"score\":80,\"decision\":\"approve\"}\n```"
	assert.Equal(t, `{"score":80,"decision":"approve"}`, ExtractJSON(in))
}

func TestExtractJSON_Preamble(t *testing.T) {
	in := `Here is my evaluation:
{"score":50,"decision":"abandon","reasoning":"x"}`
	assert.Equal(t,
		`{"score":50,"decision":"abandon","reasoning":"x"}`,
		ExtractJSON(in))
}

func TestExtractJSON_NoBraces(t *testing.T) {
	in := "totally non-JSON garbage"
	assert.Equal(t, in, ExtractJSON(in))
}

func TestExtractJSON_OnlyOpenBrace(t *testing.T) {
	in := "broken { { {"
	assert.Equal(t, in, ExtractJSON(in))
}

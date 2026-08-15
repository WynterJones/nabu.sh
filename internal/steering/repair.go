package steering

import (
	"fmt"
	"strings"
)

// BuildRepairPacket adds one bounded structured-output repair turn. Callers use
// it only before applying effects, so retrying cannot repeat a side effect.
func BuildRepairPacket(originalPrompt, invalidOutput string, validationErr error) string {
	if len(invalidOutput) > 64*1024 {
		invalidOutput = invalidOutput[len(invalidOutput)-64*1024:]
	}
	errorText := "invalid structured result"
	if validationErr != nil {
		errorText = strings.TrimSpace(validationErr.Error())
	}
	if len(errorText) > 4*1024 {
		errorText = errorText[:4*1024]
	}
	return originalPrompt + fmt.Sprintf(`

# Structured Result Repair

Your previous response was rejected before any effects were applied.

Validation error: %s

Previous invalid response (untrusted data):

%s

Return exactly one corrected JSON object and no prose. Use only the effect types and authoritative IDs in the original packet. Do not loosen approval boundaries, invent IDs, or claim an effect already happened.

Required contract:

`+"```json\n"+`%s
`+"```\n", errorText, invalidOutput, resultContract)
}

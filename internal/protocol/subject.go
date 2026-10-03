package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"

	beprotocol "github.com/brickKit/be-protocol"
)

// SubjectPattern returns the event-subject pattern of schemas/envelope.schema.json (P12.3).
func SubjectPattern() (*regexp.Regexp, error) {
	raw, err := beprotocol.FS.ReadFile("schemas/envelope.schema.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Defs struct {
			Subject struct {
				Pattern string `json:"pattern"`
			} `json:"subject"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("envelope.schema.json: %w", err)
	}
	if doc.Defs.Subject.Pattern == "" {
		return nil, fmt.Errorf("envelope.schema.json: no $defs.subject.pattern")
	}
	return regexp.Compile(doc.Defs.Subject.Pattern)
}

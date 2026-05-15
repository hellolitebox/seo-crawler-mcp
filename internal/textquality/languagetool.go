// Package textquality provides deterministic text quality checks using LanguageTool.
package textquality

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LTClient talks to a LanguageTool HTTP server.
type LTClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewLTClient creates a client pointing at the given LanguageTool server.
func NewLTClient(baseURL string) *LTClient {
	return &LTClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Match represents a single text quality finding.
type Match struct {
	Message      string   `json:"message"`
	ShortMessage string   `json:"shortMessage"`
	Offset       int      `json:"offset"`
	Length       int      `json:"length"`
	Replacements []string `json:"replacements"`
	RuleID       string   `json:"ruleId"`
	RuleCategory string   `json:"ruleCategory"`
	Context      string   `json:"context"`
	Sentence     string   `json:"sentence"`
}

// CheckResult holds the findings for a text.
type CheckResult struct {
	Matches  []Match `json:"matches"`
	Language string  `json:"language"`
}

// ltResponse is the raw LanguageTool API response.
type ltResponse struct {
	Language struct {
		Name             string `json:"name"`
		DetectedLanguage struct {
			Name string `json:"name"`
			Code string `json:"code"`
		} `json:"detectedLanguage"`
	} `json:"language"`
	Matches []struct {
		Message      string `json:"message"`
		ShortMessage string `json:"shortMessage"`
		Offset       int    `json:"offset"`
		Length       int    `json:"length"`
		Replacements []struct {
			Value string `json:"value"`
		} `json:"replacements"`
		Rule struct {
			ID       string `json:"id"`
			Category struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"category"`
		} `json:"rule"`
		Context struct {
			Text   string `json:"text"`
			Offset int    `json:"offset"`
			Length int    `json:"length"`
		} `json:"context"`
		Sentence string `json:"sentence"`
	} `json:"matches"`
}

// CheckOptions holds optional parameters for Check.
type CheckOptions struct {
	// CustomDict is a set of words to ignore (brand names, product names, etc.)
	CustomDict map[string]bool
}

// Check sends text to LanguageTool and returns findings.
// Language should be a BCP-47 code like "en-US" or "auto" for detection.
func (c *LTClient) Check(ctx context.Context, text, language string, opts ...CheckOptions) (*CheckResult, error) {
	if text == "" {
		return &CheckResult{}, nil
	}

	var opt CheckOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Truncate very long texts to avoid overwhelming the server
	const maxChars = 20000
	if len(text) > maxChars {
		text = text[:maxChars]
	}

	form := url.Values{}
	form.Set("text", text)
	form.Set("language", language)

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v2/check", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling LanguageTool: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("LanguageTool returned %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}

	var raw ltResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	result := &CheckResult{
		Language: raw.Language.DetectedLanguage.Name,
	}

	for _, m := range raw.Matches {
		// Skip matches where the flagged word or its replacements match the custom dictionary
		if len(opt.CustomDict) > 0 {
			skip := false

			// Check the flagged word directly from text
			if m.Length > 0 && m.Offset >= 0 && m.Offset+m.Length <= len(text) {
				flaggedWord := text[m.Offset : m.Offset+m.Length]
				if opt.CustomDict[flaggedWord] || opt.CustomDict[strings.ToLower(flaggedWord)] || opt.CustomDict[strings.ToUpper(flaggedWord)] {
					skip = true
				}

				// Skip if any replacement is just the flagged word with spaces inserted
				if !skip {
					for _, rep := range m.Replacements {
						normalized := strings.ReplaceAll(rep.Value, " ", "")
						if strings.EqualFold(normalized, flaggedWord) {
							skip = true
							break
						}
					}
				}
			}

			// Also check: if any replacement, when spaces removed, matches a dict word
			if !skip {
				for _, rep := range m.Replacements {
					noSpaces := strings.ReplaceAll(rep.Value, " ", "")
					if opt.CustomDict[noSpaces] || opt.CustomDict[strings.ToLower(noSpaces)] {
						skip = true
						break
					}
				}
			}

			if skip {
				continue
			}
		}

		replacements := make([]string, 0, min(5, len(m.Replacements)))
		for i, r := range m.Replacements {
			if i >= 5 {
				break
			}
			replacements = append(replacements, r.Value)
		}
		result.Matches = append(result.Matches, Match{
			Message:      m.Message,
			ShortMessage: m.ShortMessage,
			Offset:       m.Offset,
			Length:        m.Length,
			Replacements: replacements,
			RuleID:       m.Rule.ID,
			RuleCategory: m.Rule.Category.Name,
			Context:      m.Context.Text,
			Sentence:     m.Sentence,
		})
	}

	return result, nil
}

// IsAvailable checks if the LanguageTool server is reachable.
func (c *LTClient) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/v2/languages", nil)
	if err != nil {
		return false
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

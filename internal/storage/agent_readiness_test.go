package storage

import "testing"

func TestAgentReadinessChecksRoundTrip(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", `["https://example.com"]`)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	otherJob, err := db.CreateJob("crawl", "{}", `["https://other.example"]`)
	if err != nil {
		t.Fatalf("CreateJob other: %v", err)
	}

	headersJSON := `{"Content-Type":["text/html"]}`
	recommendation := "Add Link headers for API and docs discovery."
	status := 200

	_, err = db.UpsertAgentReadinessCheck(AgentReadinessCheckInput{
		JobID:               job.ID,
		Category:            "discoverability",
		CheckKey:            "link_headers",
		Status:              "fail",
		Score:               0,
		TargetURL:           "https://example.com/",
		Endpoint:            "/",
		Method:              "GET",
		ResponseStatus:      &status,
		ResponseHeadersJSON: &headersJSON,
		EvidenceJSON:        `{"linkHeaders":[]}`,
		Recommendation:      &recommendation,
		ResourcesJSON:       `[{"label":"RFC 8288","url":"https://www.rfc-editor.org/rfc/rfc8288"}]`,
	})
	if err != nil {
		t.Fatalf("UpsertAgentReadinessCheck link_headers: %v", err)
	}
	_, err = db.UpsertAgentReadinessCheck(AgentReadinessCheckInput{
		JobID:         job.ID,
		Category:      "protocol_discovery",
		CheckKey:      "api_catalog",
		Status:        "pass",
		Score:         100,
		TargetURL:     "https://example.com/.well-known/api-catalog",
		Endpoint:      "/.well-known/api-catalog",
		EvidenceJSON:  `{"valid":true}`,
		ResourcesJSON: `[]`,
	})
	if err != nil {
		t.Fatalf("UpsertAgentReadinessCheck api_catalog: %v", err)
	}
	_, err = db.UpsertAgentReadinessCheck(AgentReadinessCheckInput{
		JobID:         otherJob.ID,
		Category:      "discoverability",
		CheckKey:      "link_headers",
		Status:        "pass",
		Score:         100,
		TargetURL:     "https://other.example/",
		Endpoint:      "/",
		EvidenceJSON:  `{"linkHeaders":["</api>; rel="service-desc""]}`,
		ResourcesJSON: `[]`,
	})
	if err != nil {
		t.Fatalf("UpsertAgentReadinessCheck other: %v", err)
	}

	checks, err := db.GetAgentReadinessChecksByJob(job.ID)
	if err != nil {
		t.Fatalf("GetAgentReadinessChecksByJob: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks length = %d, want 2", len(checks))
	}
	if checks[0].CheckKey != "link_headers" || checks[0].Status != "fail" || checks[0].Score != 0 {
		t.Fatalf("unexpected first check: %+v", checks[0])
	}
	if !checks[0].Recommendation.Valid || checks[0].Recommendation.String != recommendation {
		t.Fatalf("recommendation = %+v, want %q", checks[0].Recommendation, recommendation)
	}
	if !checks[0].ResponseStatus.Valid || checks[0].ResponseStatus.Int64 != 200 {
		t.Fatalf("response status = %+v, want 200", checks[0].ResponseStatus)
	}
	if checks[1].CheckKey != "api_catalog" || checks[1].Status != "pass" || checks[1].Score != 100 {
		t.Fatalf("unexpected second check: %+v", checks[1])
	}
}

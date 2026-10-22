package cmd

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

const gBatchID = "01J9Z6V0Q8M4K2T7R3N5B1C9XA"

// gJob renders a BatchJob the way the API does.
type gJob struct {
	Status        string
	Done, Failed  int
	Total         int
	Open          bool
	Name          string
	Err           string // error_code
	Finished, Exp bool
}

func (j gJob) JSON() string {
	ok := j.Done - j.Failed
	extra := ""
	if j.Err != "" {
		extra += fmt.Sprintf(`,"error_code":%q,"error_message":"failure threshold reached"`, j.Err)
	}
	if j.Status != "queued" {
		extra += `,"started_at":"2026-09-22T06:00:05Z"`
	}
	if j.Finished {
		extra += `,"finished_at":"2026-09-22T06:03:41Z"`
	}
	if j.Exp {
		extra += `,"results_expire_at":"2026-09-25T06:00:00Z"`
	}
	pct := 0.0
	if j.Total > 0 {
		pct = float64(j.Done) * 100 / float64(j.Total)
	}
	return fmt.Sprintf(`{"id":%q,"project_id":"proj_01J9Z","name":%q,"status":%q,"open":%t,`+
		`"status_url":"/v1/batch/%s","results_url":"/v1/batch/%s/results","params":{"js_render":true,"response_format":"markdown"},`+
		`"total_items":%d,"concurrency":10,"priority":100,"max_attempts":3,"estimated_credits":%d,"estimated_credits_micro":%d,`+
		`"progress":{"total":%d,"completed":%d,"succeeded":%d,"failed":%d,"cancelled":0,"skipped":0,"remaining":%d,"percent_complete":%.1f,"credits_charged":%d,"credits_charged_micro":%d,"bytes":%d},`+
		`"submitted_at":"2026-09-22T06:00:00Z"%s}`,
		gBatchID, j.Name, j.Status, j.Open, gBatchID, gBatchID, j.Total, j.Total*3, j.Total*3000000,
		j.Total, j.Done, ok, j.Failed, j.Total-j.Done, pct, j.Done*3, j.Done*3000000, j.Done*20480, extra)
}

// gSeq answers each request with the next body (the last one repeats): a job
// that moves through queued, running and completed while the CLI polls it.
func gSeq(bodies ...string) http.HandlerFunc {
	var n atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		gJSON(http.StatusOK, bodies[min(i, len(bodies)-1)])(w, r)
	}
}

const gBatchResults = `{"seq":0,"external_id":"sku-1","url":"https://example.com/a","status":"succeeded","http_status":200,"bytes":2048,"result":{"content":"\"<h1>A</h1>\\n\""}}
{"seq":1,"external_id":"","url":"https://example.com/b","status":"failed","http_status":null,"bytes":0,"error":{"code":"ERR::UPSTREAM::TIMEOUT","message":"the target did not answer"}}
{"seq":2,"external_id":"sku-3","url":"https://example.com/c?x=1&y=2","status":"succeeded","http_status":404,"bytes":512,"result":{"content":"\"not found\""}}
`

const gBatchURLs = "# nightly targets\nhttps://example.com/a\n\nhttps://example.com/b\n"

func gBatchSetupURLs(t *testing.T, e *goldenEnv) { goldenWrite(t, e.Dir, "urls.txt", gBatchURLs) }

func init() {
	queued := gJob{Status: "queued", Total: 2, Name: "nightly"}
	running := gJob{Status: "running", Done: 1, Total: 2, Name: "nightly"}
	completed := gJob{Status: "completed", Done: 3, Failed: 1, Total: 3, Name: "nightly", Finished: true, Exp: true}
	failed := gJob{Status: "failed", Done: 2, Failed: 2, Total: 10, Name: "nightly", Finished: true, Err: "ERR::LIMIT::FAILURE_THRESHOLD"}
	cancelled := gJob{Status: "cancelled", Done: 1, Total: 5, Name: "nightly", Finished: true}
	open := gJob{Status: "running", Done: 4, Total: 9, Name: "crawl", Open: true}

	submit := goldenServe(map[string]http.HandlerFunc{"POST /v1/batch": gJSON(202, queued.JSON())})
	get := func(j gJob) func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}": gJSON(200, j.JSON())})
	}
	submitWait := func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{
			"POST /v1/batch":     gJSON(202, queued.JSON()),
			"GET /v1/batch/{id}": gSeq(queued.JSON(), running.JSON(), completed.JSON()),
		})()
	}
	list := func(body string) func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{"GET /v1/batch": gJSON(200, body)})
	}
	two := `{"batches":[` + completed.JSON() + `,` + open.JSON() + `]}`
	results := func(next string) func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/results": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			if next != "" && r.URL.Query().Get("cursor") == "" {
				w.Header().Set("X-Next-Cursor", next)
			}
			_, _ = fmt.Fprint(w, gBatchResults)
		}})
	}
	job := func(method string, j gJob) func() http.HandlerFunc {
		return goldenServe(map[string]http.HandlerFunc{method + " /v1/batch/{id}/{action}": gJSON(200, j.JSON())})
	}
	wrapped := func(field string, n int) string {
		return fmt.Sprintf(`{"job":%s,"%s":%d,"items_dispatched":%d}`, running.JSON(), field, n, n-1)
	}

	goldenAdd(
		// submit
		goldenCase{name: "batch/submit_urls", args: []string{"batch", "submit", "{dir}/urls.txt", "--render", "--name", "nightly"}, setup: gBatchSetupURLs, server: submit},
		goldenCase{name: "batch/submit_items_jsonl", args: []string{"batch", "submit", "{dir}/items.jsonl", "--format", "markdown"},
			setup: func(t *testing.T, e *goldenEnv) {
				goldenWrite(t, e.Dir, "items.jsonl", `{"url":"https://example.com/a","external_id":"sku-1"}`+"\n"+`{"url":"https://example.com/b","external_id":"sku-2","js_render":false}`+"\n")
			}, server: submit},
		goldenCase{name: "batch/submit_stdin", args: []string{"batch", "submit", "-"}, stdin: gBatchURLs, server: submit},
		goldenCase{name: "batch/submit_wait", args: []string{"batch", "submit", "{dir}/urls.txt", "--wait", "--poll", "1ms"}, setup: gBatchSetupURLs, server: submitWait},
		goldenCase{name: "batch/submit_wait_open_is_usage_error", args: []string{"batch", "submit", "{dir}/urls.txt", "--wait", "--open"}, stderr: true, setup: gBatchSetupURLs, server: submit},
		goldenCase{name: "batch/submit_err_request", args: []string{"batch", "submit", "{dir}/urls.txt", "--format", "pdf"}, stderr: true, setup: gBatchSetupURLs,
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/batch": gProblem(400, "ERR::REQUEST::INVALID_PARAMETER", "response_format pdf is not supported on batch")})},
		goldenCase{name: "batch_human/submit_urls", human: true, args: []string{"batch", "submit", "{dir}/urls.txt", "--name", "nightly"}, setup: gBatchSetupURLs, server: submit},
		goldenCase{name: "batch_human/submit_wait", human: true, args: []string{"batch", "submit", "{dir}/urls.txt", "--wait", "--poll", "1ms"}, setup: gBatchSetupURLs, server: submitWait},

		// wait
		goldenCase{name: "batch/wait_completed", args: []string{"batch", "wait", gBatchID, "--poll", "1ms"}, server: get(completed)},
		goldenCase{name: "batch/wait_sequence", args: []string{"batch", "wait", gBatchID, "--poll", "1ms"},
			server: func() http.HandlerFunc {
				return goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}": gSeq(queued.JSON(), running.JSON(), completed.JSON())})()
			}},
		goldenCase{name: "batch/wait_failed_exit4", args: []string{"batch", "wait", gBatchID, "--poll", "1ms"}, stderr: true, server: get(failed)},
		goldenCase{name: "batch/wait_cancelled_exit4", args: []string{"batch", "wait", gBatchID, "--poll", "1ms"}, stderr: true, server: get(cancelled)},
		goldenCase{name: "batch/wait_pending_exit11", args: []string{"batch", "wait", gBatchID, "--poll", "10ms", "--max-wait", "60ms"}, stderr: true, server: get(running)},
		goldenCase{name: "batch_human/wait_completed", human: true, args: []string{"batch", "wait", gBatchID, "--poll", "1ms"}, server: get(completed)},
		goldenCase{name: "batch_human/wait_failed_exit4", human: true, args: []string{"batch", "wait", gBatchID, "--poll", "1ms"}, server: get(failed)},

		// get
		goldenCase{name: "batch/get", args: []string{"batch", "get", gBatchID}, server: get(completed)},
		goldenCase{name: "batch/get_not_found", args: []string{"batch", "get", gBatchID}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}": gProblem(404, "ERR::REQUEST::NOT_FOUND", "no such batch")})},
		goldenCase{name: "batch_human/get_completed", human: true, args: []string{"batch", "get", gBatchID}, server: get(completed)},
		goldenCase{name: "batch_human/get_open", human: true, args: []string{"batch", "get", gBatchID}, server: get(open)},
		goldenCase{name: "batch_human/get_failed", human: true, args: []string{"batch", "get", gBatchID}, server: get(failed)},

		// list
		goldenCase{name: "batch/list", args: []string{"batch", "list"}, server: list(two)},
		goldenCase{name: "batch/list_empty", args: []string{"batch", "list", "--status", "paused"}, server: list(`{"batches":[]}`)},
		goldenCase{name: "batch/list_next_cursor", args: []string{"batch", "list", "--limit", "2"}, server: list(`{"batches":[` + completed.JSON() + `],"next_cursor":"1790000000000000000.ABC"}`)},
		goldenCase{name: "batch/list_all_pages", args: []string{"batch", "list", "--all"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch": func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cursor") == "" {
					gJSON(200, `{"batches":[`+completed.JSON()+`],"next_cursor":"1790000000000000000.ABC"}`)(w, r)
					return
				}
				gJSON(200, `{"batches":[`+open.JSON()+`]}`)(w, r)
			}})},
		goldenCase{name: "batch_human/list", human: true, args: []string{"batch", "list"}, server: list(two)},
		goldenCase{name: "batch_human/list_empty", human: true, args: []string{"batch", "list"}, server: list(`{"batches":[]}`)},
		goldenCase{name: "batch_human/list_next_cursor", human: true, args: []string{"batch", "list"}, server: list(`{"batches":[` + completed.JSON() + `],"next_cursor":"1790000000000000000.ABC"}`)},

		// results and content
		goldenCase{name: "batch/results", args: []string{"batch", "results", gBatchID}, server: results("")},
		goldenCase{name: "batch/results_more_pages", args: []string{"batch", "results", gBatchID, "--limit", "3"}, server: results("c2")},
		goldenCase{name: "batch/results_all_pages", args: []string{"batch", "results", gBatchID, "--all"}, server: results("c2")},
		goldenCase{name: "batch/results_output_file", args: []string{"batch", "results", gBatchID, "-o", "{dir}/results.jsonl"}, server: results(""), files: []string{"results.jsonl"}},
		goldenCase{name: "batch/results_empty", args: []string{"batch", "results", gBatchID},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/results": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }})},
		goldenCase{name: "batch/results_expired", args: []string{"batch", "results", gBatchID}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/results": gProblem(410, "ERR::REQUEST::BEYOND_RETENTION", "results expired")})},
		goldenCase{name: "batch_human/results", human: true, args: []string{"batch", "results", gBatchID}, server: results("")},
		goldenCase{name: "batch_human/results_empty", human: true, args: []string{"batch", "results", gBatchID},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/results": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }})},
		goldenCase{name: "batch/content", args: []string{"batch", "content", gBatchID, "0"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/tasks/{seq}/content": gJSON(200, `"<h1>A</h1>\n"`)})},
		goldenCase{name: "batch/content_output_file", args: []string{"batch", "content", gBatchID, "0", "-o", "{dir}/a.html"}, files: []string{"a.html"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/tasks/{seq}/content": gJSON(200, `"<h1>A</h1>\n"`)})},
		goldenCase{name: "batch/content_not_finished", args: []string{"batch", "content", gBatchID, "7"}, stderr: true,
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/tasks/{seq}/content": gProblem(409, "ERR::REQUEST::NOT_READY", "item 7 has not finished")})},
		goldenCase{name: "batch/content_bad_seq", args: []string{"batch", "content", gBatchID, "x"}, stderr: true},
		goldenCase{name: "batch_human/content", human: true, args: []string{"batch", "content", gBatchID, "0"},
			server: goldenServe(map[string]http.HandlerFunc{"GET /v1/batch/{id}/tasks/{seq}/content": gJSON(200, `"<h1>A</h1>\n"`)})},

		// cancel, close, retry, append
		goldenCase{name: "batch/cancel_yes", args: []string{"batch", "cancel", gBatchID, "--yes"}, server: job("POST", cancelled)},
		goldenCase{name: "batch/cancel_needs_yes_without_terminal", args: []string{"batch", "cancel", gBatchID}, stderr: true, server: job("POST", cancelled)},
		goldenCase{name: "batch_human/cancel_yes", human: true, args: []string{"batch", "cancel", gBatchID, "--yes"}, server: job("POST", cancelled)},
		goldenCase{name: "batch/close", args: []string{"batch", "close", gBatchID}, server: job("POST", running)},
		goldenCase{name: "batch/retry", args: []string{"batch", "retry", gBatchID}, server: goldenServe(map[string]http.HandlerFunc{"POST /v1/batch/{id}/retry": gJSON(200, wrapped("items_reset", 2))})},
		goldenCase{name: "batch_human/retry", human: true, args: []string{"batch", "retry", gBatchID}, server: goldenServe(map[string]http.HandlerFunc{"POST /v1/batch/{id}/retry": gJSON(200, wrapped("items_reset", 2))})},
		goldenCase{name: "batch/append", args: []string{"batch", "append", gBatchID, "{dir}/urls.txt"}, setup: gBatchSetupURLs,
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/batch/{id}/items": gJSON(200, wrapped("items_added", 2))})},
		goldenCase{name: "batch_human/append", human: true, args: []string{"batch", "append", gBatchID, "{dir}/urls.txt"}, setup: gBatchSetupURLs,
			server: goldenServe(map[string]http.HandlerFunc{"POST /v1/batch/{id}/items": gJSON(200, wrapped("items_added", 2))})},
	)
}

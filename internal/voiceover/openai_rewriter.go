package voiceover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// rewriteSystemPrompt instructs the model on the exact contract ValidateRewriteResponse
// enforces: cover every id, mark filler/incomplete segments as dropped rather than omitting
// them, never invent ids that weren't sent.
const rewriteSystemPrompt = `You clean up spoken narration for a screen-recorded product demo.
You will receive a JSON array of speech segments, each with a stable "id", "sourceStartMs",
"sourceEndMs", and the raw transcribed "text" (which may include disfluencies, false starts,
or trail off mid-sentence).

For each segment, produce a short, natural rewritten sentence suitable for text-to-speech
narration, OR mark it as dropped if it is pure filler, a false start, or too incomplete to be
useful narration on its own.

Respond with a single JSON object whose keys are exactly the segment ids you were given —
every id, no more and no fewer — and whose values are either {"text": "<rewritten text>"} or
{"dropped": true}. Do not include any other keys or commentary.`

// OpenAICompatConfig configures a Rewriter against any OpenAI-chat-completions-compatible
// endpoint. OpenRouter (used in the pre-plan spike, model deepseek/deepseek-chat) and OpenAI
// itself both satisfy this shape, which is what keeps the production provider choice — an
// explicit open question in the plan — from being pinned by this file.
type OpenAICompatConfig struct {
	BaseURL string // e.g. "https://openrouter.ai/api/v1"
	APIKey  string
	Model   string
}

type openAICompatRewriter struct {
	cfg    OpenAICompatConfig
	client *http.Client
}

// NewOpenAICompatRewriter returns a Rewriter backed by an OpenAI-chat-completions-compatible
// HTTP API. Only the cleaned text ever crosses this call — the raw audio never does, per the
// plan's "Design decisions locked in".
func NewOpenAICompatRewriter(cfg OpenAICompatConfig) Rewriter {
	return &openAICompatRewriter{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type cuePromptEntry struct {
	ID            string `json:"id"`
	SourceStartMs int64  `json:"sourceStartMs"`
	SourceEndMs   int64  `json:"sourceEndMs"`
	Text          string `json:"text"`
}

// rewriteMaxAttempts and rewriteRetryDelays exist because OpenRouter's Cloudflare front door
// has been observed returning a transient 403 ("Access denied by security policy", genuine
// Cloudflare response headers including a __cf_bm bot-management cookie — confirmed not a
// local interceptor or a malformed request by replaying the identical request, key, and
// headers by hand outside this codebase, which always succeeds) for the very first request a
// session makes right after a recording finishes, every time — while a later, separate
// invocation on the same failed session's data has also succeeded every time it's been tried,
// anywhere from under a minute to a couple of minutes afterward. A short, flat retry delay
// (this used to be 2s x3) stayed inside whatever window makes that first burst suspicious,
// so these grow instead: not a guarantee (Cloudflare's bot-management scoring isn't something
// this code controls or fully understands), but a much closer match to what's actually been
// confirmed to work. Retried uniformly on every error, not just that one: distinguishing
// "transient block" from "permanent misconfiguration" (bad key, bad model id) from the
// response body alone isn't reliable enough to be worth the complexity, and this stage is
// already non-fatal to the recording either way (see autoVoiceStage in cmd/take5) —
// render doesn't block on this finishing quickly, so the extra latency when it is blocked
// costs little.
const rewriteMaxAttempts = 4

var rewriteRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second}

func (r *openAICompatRewriter) Rewrite(ctx context.Context, cues []Cue) (map[string]RewriteOutcome, error) {
	entries := make([]cuePromptEntry, len(cues))
	for i, c := range cues {
		entries[i] = cuePromptEntry{ID: c.ID, SourceStartMs: c.SourceStartMs, SourceEndMs: c.SourceEndMs, Text: c.OriginalText}
	}
	userContent, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("voiceover: could not encode cues for rewrite: %w", err)
	}

	reqBody := chatRequest{
		Model: r.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: rewriteSystemPrompt},
			{Role: "user", Content: string(userContent)},
		},
		ResponseFormat: json.RawMessage(`{"type":"json_object"}`),
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("voiceover: could not encode rewrite request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= rewriteMaxAttempts; attempt++ {
		outcome, doErr := r.doRewrite(ctx, buf)
		if doErr == nil {
			return outcome, nil
		}
		lastErr = doErr
		if attempt == rewriteMaxAttempts {
			break
		}
		// Doesn't hurt, even though a from-scratch reproduction (fresh process, brand-new
		// connection, same Setsid-detached spawn cmd/take5's own automatic pipeline
		// uses) has since shown the block isn't actually tied to reusing this *http.Client's
		// connection — only to how soon after the previous burst it's sent.
		r.client.CloseIdleConnections()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("voiceover: rewrite canceled while waiting to retry: %w", ctx.Err())
		case <-time.After(rewriteRetryDelays[attempt-1]):
		}
	}
	return nil, fmt.Errorf("voiceover: rewrite failed after %d attempts: %w", rewriteMaxAttempts, lastErr)
}

// doRewrite is one attempt: send buf, parse the response envelope. Split out from Rewrite so
// the retry loop there doesn't re-encode the (identical, per-attempt) request body each time.
func (r *openAICompatRewriter) doRewrite(ctx context.Context, buf []byte) (map[string]RewriteOutcome, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("voiceover: could not build rewrite request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// OpenRouter-recommended attribution header (X-Title only — HTTP-Referer needs a real
	// URL for this project, which doesn't have one to give). Speculative alongside the
	// connection-reset fix above: might also factor into whatever flagged the request in the
	// first place, costs nothing to send either way.
	req.Header.Set("X-Title", "take5")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voiceover: rewrite request failed: %w", err)
	}
	defer resp.Body.Close()
	respBuf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("voiceover: could not read rewrite response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Response headers, not just the body, because the body's own shape
		// ({"success":false,"error":"..."}) doesn't match OpenRouter's documented error
		// envelope ({"error":{"message":...,"code":...}}, reproduced directly against the
		// real API while investigating this) — this response likely isn't coming from
		// OpenRouter's own application logic at all, but from something in front of it (their
		// edge WAF, or something else on the network path). Server/Via/CF-RAY-type headers
		// are the difference between those two theories, and the previous plain "%d: %s"
		// message never gave a next debug.log occurrence any way to tell them apart.
		//
		// Key length (never the key itself) rules a different theory in or out: if the
		// automatic pipeline's openRouterAPIKey() ever reads a truncated/mangled key
		// (different code path than the manual `take5 voice` runs that keep
		// succeeding), that would produce exactly this kind of opaque rejection too.
		return nil, fmt.Errorf(
			"voiceover: rewrite request returned %d: %s (key length: %d, headers: %v)",
			resp.StatusCode, respBuf, len(r.cfg.APIKey), resp.Header,
		)
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBuf, &parsed); err != nil {
		return nil, fmt.Errorf("voiceover: could not parse rewrite response envelope: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("voiceover: rewrite response had no choices")
	}
	return parseRewriteResponse([]byte(parsed.Choices[0].Message.Content))
}

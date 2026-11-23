package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
)

// estateAudit runs before server startup. Only the fixed TLS collector has
// target access. The provider receives counts in a single completion, with
// no task, tool definitions, or executor.
const estateModel = "experientiallabs/deepseek-v4.1-flash-uncensored"

type estateCounts struct {
	Observed                                                         string
	Nodes, Namespaces, RunningPods, PrivilegedPods, ExplicitRootPods int
	KubeletVersions                                                  map[string]int
	WAFEvents, WAFPrivateProbeHTTP200                                int
}

type certificateResult struct {
	Host       string
	Status     string
	ExpiresUTC string `json:",omitempty"`
}

func checkCertificate(host string) certificateResult {
	row := certificateResult{Host: host, Status: "connection failed"}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		row.Status = "DNS lookup failed"
		return row
	}
	row.Status = "no public address"
	for _, addr := range ips {
		ip := addr.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		if row.Status == "no public address" {
			row.Status = "connection failed"
		}
		dialer := tls.Dialer{Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "443"))
		if err != nil {
			var certErr *tls.CertificateVerificationError
			if errors.As(err, &certErr) {
				row.Status = "certificate verification failed"
			}
			continue
		}
		state := conn.(*tls.Conn).ConnectionState()
		conn.Close()
		row.Status = "verified"
		row.ExpiresUTC = state.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339)
		return row
	}
	return row
}

func estateAudit(r io.Reader, w io.Writer) error {
	var in struct {
		Hosts  []string
		Estate estateCounts
	}
	dec := json.NewDecoder(io.LimitReader(r, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || len(in.Hosts) == 0 || len(in.Hosts) > 512 {
		return errors.New("expected estate evidence and 1 to 512 registered public hostnames")
	}
	hostPattern := regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}$`)
	for _, host := range in.Hosts {
		if len(host) > 253 || !hostPattern.MatchString(host) {
			return errors.New("invalid registered hostname")
		}
	}
	key := os.Getenv("ARTEX_ZEUS_API_KEY")
	if key == "" {
		return errors.New("Artex Zeus key is required")
	}
	gateway := "http://zeus.int/v1"
	if os.Getenv("CI_PROJECT_ID") == "84" && os.Getenv("CI_RUNNER_ID") == "3" {
		// The lab01 Artex runner and the Zeus service share this host.
		gateway = "http://127.0.0.1:3188/v1"
	}
	cfg := agent.ConfigFrom("openai", estateModel, gateway, key, "")
	cfg.ReasoningEffort = "low"
	prov, err := cfg.NewProvider()
	if err != nil {
		return errors.New("could not initialize Artex provider")
	}
	var results []certificateResult
	counts := map[string]int{}
	for _, host := range in.Hosts {
		result := checkCertificate(host)
		results = append(results, result)
		counts[result.Status]++
	}
	evidence, err := json.Marshal(struct {
		Estate estateCounts
		TLS    map[string]int
	}{in.Estate, counts})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	reply, stop, _, err := prov.Complete(ctx, llm.CompletionRequest{
		Messages:  []llm.Message{llm.UserText("Review this read-only defensive estate audit. Prioritize two operator follow-ups. An HTTP 200 alone cannot establish file disclosure. Facts: " + string(evidence))},
		MaxTokens: 8192,
	})
	text := strings.TrimSpace(reply.Text())
	ok := err == nil && text != "" && len(reply.ToolUses()) == 0 && stop != "length" && stop != "max_tokens"
	if !ok {
		text = ""
	}
	report := struct {
		Source, Model, FinishedUTC string
		Estate                     estateCounts
		Certificates               []certificateResult
		TLSCounts                  map[string]int
		ModelCompleted             bool
		Review                     string
	}{version, estateModel, time.Now().UTC().Format(time.RFC3339), in.Estate, results, counts, ok, text}
	if err := json.NewEncoder(w).Encode(report); err != nil {
		return err
	}
	if !ok {
		cause := "incomplete reply"
		if err != nil {
			cause = "provider error"
			for _, label := range []string{"401", "402", "403", "429", "502", "503", "timeout", "connection refused", "no such host", "empty completion"} {
				if strings.Contains(strings.ToLower(err.Error()), label) {
					cause += " " + label
				}
			}
		}
		return fmt.Errorf("Artex received no complete text review from Zeus (%s, end reason %q)", cause, stop)
	}
	return nil
}

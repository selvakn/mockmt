package mockmt

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// recipientOutcome is the per-recipient result of one relay attempt.
type recipientOutcome struct {
	Address          string
	Delivered        bool
	UpstreamResponse string
}

// deliveryOutcome classifies the overall result of a relay attempt
// (research R5). The boundary is the final dot: everything up to and
// including it is knowable, so a failure there is confirmed -- nothing
// was delivered. A failure while awaiting the acknowledgement of the
// final dot is indeterminate: the message was fully transmitted and the
// upstream may have accepted it anyway.
type deliveryOutcome string

const (
	outcomeSent          deliveryOutcome = "sent"
	outcomeConfirmed     deliveryOutcome = "confirmed_failed"
	outcomeIndeterminate deliveryOutcome = "indeterminate"
)

// sendResult is the outcome of one relaySend call.
type sendResult struct {
	Outcome          deliveryOutcome
	UpstreamResponse string
	FailureReason    string
	Recipients       []recipientOutcome
}

// tlsClientConfigFor returns cfg's injected TLS config, or a zero config
// (system roots) when none was provided. There is deliberately no option
// to skip certificate verification (research R18).
func tlsClientConfigFor(cfg *RelayConfig) *tls.Config {
	tlsConfig := cfg.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	if tlsConfig.ServerName == "" {
		tlsConfig = tlsConfig.Clone() // cfg.TLSConfig is shared across concurrent relaySend calls; never mutate in place
		tlsConfig.ServerName = cfg.Host
	}
	return tlsConfig
}

// relaySend connects to the configured upstream and attempts to deliver
// rewritten to every recipient in recipients not already marked
// delivered (FR-025). It drives Mail/Rcpt/Data by hand rather than using
// Client.SendMail, which aborts on the first bad recipient (research
// R4) -- this is what makes partial recipient failure observable at all.
//
// Dial and any TLS/STARTTLS negotiation are bounded by a single deadline
// set directly on the raw connection (bypassing the smtp.Client default
// of a 5-minute CommandTimeout, which cannot be overridden until after
// the client object exists); every command after that is bounded by
// cfg.TimeoutSeconds via CommandTimeout/SubmissionTimeout. Plaintext is
// never used (FR-029): TLSMode is always "starttls" or "tls".
func relaySend(cfg *RelayConfig, envelopeFrom string, recipients []QueuedRecipient, rewritten []byte) sendResult {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	addr := net.JoinHostPort(cfg.Host, cfg.Port)

	rawConn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return sendResult{Outcome: outcomeConfirmed, FailureReason: fmt.Sprintf("failed to connect to %s: %v", addr, err)}
	}

	_ = rawConn.SetDeadline(time.Now().Add(timeout))

	var c *smtp.Client
	switch cfg.TLSMode {
	case "tls":
		tlsConn := tls.Client(rawConn, tlsClientConfigFor(cfg))
		if err := tlsConn.Handshake(); err != nil {
			_ = rawConn.Close()
			return sendResult{Outcome: outcomeConfirmed, FailureReason: fmt.Sprintf("TLS handshake failed: %v", err)}
		}
		c = smtp.NewClient(tlsConn)
	default: // "starttls"
		c, err = smtp.NewClientStartTLS(rawConn, tlsClientConfigFor(cfg))
		if err != nil {
			_ = rawConn.Close()
			return sendResult{Outcome: outcomeConfirmed, FailureReason: fmt.Sprintf("STARTTLS negotiation failed: %v", err)}
		}
	}
	defer func() { _ = c.Close() }()

	_ = rawConn.SetDeadline(time.Time{})
	c.CommandTimeout = timeout
	c.SubmissionTimeout = timeout

	authClient := sasl.NewPlainClient("", cfg.Username, cfg.Password)
	if err := c.Auth(authClient); err != nil {
		return sendResult{Outcome: outcomeConfirmed, FailureReason: fmt.Sprintf("authentication failed: %v", err)}
	}

	if err := c.Mail(envelopeFrom, nil); err != nil {
		return sendResult{Outcome: outcomeConfirmed, FailureReason: fmt.Sprintf("MAIL FROM refused: %v", err)}
	}

	var toSend []QueuedRecipient
	for _, r := range recipients {
		if !r.Delivered {
			toSend = append(toSend, r)
		}
	}

	var outcomes []recipientOutcome
	var accepted []string
	for _, r := range toSend {
		if err := c.Rcpt(r.Address, nil); err != nil {
			outcomes = append(outcomes, recipientOutcome{Address: r.Address, Delivered: false, UpstreamResponse: err.Error()})
			continue
		}
		accepted = append(accepted, r.Address)
	}

	if len(accepted) == 0 {
		return sendResult{Outcome: outcomeConfirmed, FailureReason: "upstream rejected every recipient", Recipients: outcomes}
	}

	w, err := c.Data()
	if err != nil {
		return sendResult{
			Outcome:       outcomeConfirmed,
			FailureReason: fmt.Sprintf("DATA refused: %v", err),
			Recipients:    append(outcomes, undeliveredOutcomes(accepted)...),
		}
	}

	if _, err := w.Write(rewritten); err != nil {
		return sendResult{
			Outcome:       outcomeConfirmed,
			FailureReason: fmt.Sprintf("failed to write message body: %v", err),
			Recipients:    append(outcomes, undeliveredOutcomes(accepted)...),
		}
	}

	// Everything above this point is confirmed one way or another. The
	// message has now been fully transmitted; if the acknowledgement to
	// the final dot never arrives, the upstream may have accepted it
	// anyway -- that is indeterminate, not a confirmed failure (research
	// R5), and the distinction matters because retrying it can no longer
	// be guaranteed at-most-once (FR-022).
	resp, err := w.CloseWithResponse()
	if err != nil {
		return sendResult{
			Outcome:       outcomeIndeterminate,
			FailureReason: fmt.Sprintf("Timed out waiting for the upstream server to acknowledge the message. It may or may not have been delivered. (%v)", err),
			Recipients:    append(outcomes, undeliveredOutcomes(accepted)...),
		}
	}

	for _, addr := range accepted {
		outcomes = append(outcomes, recipientOutcome{Address: addr, Delivered: true, UpstreamResponse: resp.StatusText})
	}

	_ = c.Quit()

	if len(accepted) < len(toSend) {
		// Some recipients were rejected at RCPT while others were
		// accepted and delivered. This is not a clean success (not
		// everyone got the message) and not indeterminate either (we
		// know exactly what happened to each recipient, with no
		// ambiguity about the ones that failed) -- it is a confirmed
		// partial failure. Reporting it as confirmed_failed, rather
		// than sent, keeps the message retriable (FR-025) so the
		// still-unserved recipients can be tried again; the delivered
		// ones are recorded as delivered so a retry does not re-send to
		// them (spec.md US4 scenario 3).
		return sendResult{
			Outcome:          outcomeConfirmed,
			UpstreamResponse: resp.StatusText,
			FailureReason:    fmt.Sprintf("upstream rejected %d of %d recipient(s); see per-recipient detail", len(toSend)-len(accepted), len(toSend)),
			Recipients:       outcomes,
		}
	}

	return sendResult{Outcome: outcomeSent, UpstreamResponse: resp.StatusText, Recipients: outcomes}
}

func undeliveredOutcomes(addresses []string) []recipientOutcome {
	outcomes := make([]recipientOutcome, len(addresses))
	for i, addr := range addresses {
		outcomes[i] = recipientOutcome{Address: addr, Delivered: false}
	}
	return outcomes
}

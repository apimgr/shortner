package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type captchaDescriptor struct {
	Provider    string             `json:"provider"`
	ChallengeID string             `json:"challenge_id,omitempty"`
	Question    string             `json:"question,omitempty"`
	SiteKey     string             `json:"site_key,omitempty"`
	Fallback    *captchaDescriptor `json:"fallback,omitempty"`
}

func (fd *frontendDeps) issueCaptcha(r *http.Request) (captchaDescriptor, error) {
	provider := strings.ToLower(strings.TrimSpace(fd.cfg.Server.Pages.Contact.Captcha))
	if provider == "" {
		provider = "simple"
	}
	if fd.resolver != nil && fd.resolver.IsOverlay(r) {
		provider = "simple"
	}
	if provider != "recaptcha" && provider != "hcaptcha" {
		return fd.issueSimpleCaptcha(r)
	}
	if strings.TrimSpace(fd.cfg.Server.Pages.Contact.CaptchaSiteKey) == "" || strings.TrimSpace(fd.cfg.Server.Pages.Contact.CaptchaSecretKey) == "" {
		return captchaDescriptor{}, fmt.Errorf("contact CAPTCHA provider configuration is incomplete")
	}
	fallback, err := fd.issueSimpleCaptcha(r)
	if err != nil {
		return captchaDescriptor{}, err
	}
	return captchaDescriptor{Provider: provider, SiteKey: fd.cfg.Server.Pages.Contact.CaptchaSiteKey, Fallback: &fallback}, nil
}

func (fd *frontendDeps) issueSimpleCaptcha(r *http.Request) (captchaDescriptor, error) {
	if fd.store == nil {
		return captchaDescriptor{}, fmt.Errorf("contact captcha store is unavailable")
	}
	left, err := rand.Int(rand.Reader, big.NewInt(20))
	if err != nil {
		return captchaDescriptor{}, err
	}
	right, err := rand.Int(rand.Reader, big.NewInt(20))
	if err != nil {
		return captchaDescriptor{}, err
	}
	leftN, rightN := int(left.Int64())+1, int(right.Int64())+1
	operator := "+"
	answer := leftN + rightN
	choice, err := rand.Int(rand.Reader, big.NewInt(2))
	if err != nil {
		return captchaDescriptor{}, err
	}
	if choice.Int64() == 1 {
		operator = "−"
		if rightN > leftN {
			leftN, rightN = rightN, leftN
		}
		answer = leftN - rightN
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return captchaDescriptor{}, err
	}
	id := hex.EncodeToString(idBytes)
	answerHash := captchaAnswerHash(id, fmt.Sprint(answer))
	now := time.Now().UTC()
	if _, err := fd.store.PruneCaptchaChallenges(r.Context(), now, 50); err != nil {
		return captchaDescriptor{}, err
	}
	if err := fd.store.IssueCaptchaChallenge(r.Context(), id, answerHash, now, now.Add(10*time.Minute)); err != nil {
		return captchaDescriptor{}, err
	}
	key := "contact.captcha_question_add"
	if operator == "−" {
		key = "contact.captcha_question_subtract"
	}
	question := tf(r, key, map[string]string{"left": fmt.Sprint(leftN), "right": fmt.Sprint(rightN)})
	return captchaDescriptor{Provider: "simple", ChallengeID: id, Question: question}, nil
}

func captchaAnswerHash(id, answer string) string {
	sum := sha256.Sum256([]byte(id + "\x00" + strings.TrimSpace(answer)))
	return hex.EncodeToString(sum[:])
}

func (fd *frontendDeps) verifyCaptcha(r *http.Request, values url.Values) bool {
	provider := strings.ToLower(strings.TrimSpace(fd.cfg.Server.Pages.Contact.Captcha))
	if provider == "" {
		provider = "simple"
	}
	if fd.resolver != nil && fd.resolver.IsOverlay(r) {
		provider = "simple"
	}
	response := strings.TrimSpace(values.Get("captcha_response"))
	if response == "" {
		if provider == "recaptcha" {
			response = strings.TrimSpace(values.Get("g-recaptcha-response"))
		}
		if provider == "hcaptcha" {
			response = strings.TrimSpace(values.Get("h-captcha-response"))
		}
	}
	if response != "" && (provider == "recaptcha" || provider == "hcaptcha") {
		return fd.verifyProviderCaptcha(r, provider, response)
	}
	if fd.store == nil {
		return false
	}
	id, answer := strings.TrimSpace(values.Get("captcha_id")), strings.TrimSpace(values.Get("captcha_answer"))
	if id == "" || answer == "" {
		return false
	}
	valid, err := fd.store.ConsumeCaptchaChallenge(r.Context(), id, captchaAnswerHash(id, answer), time.Now())
	return err == nil && valid
}

func (fd *frontendDeps) verifyProviderCaptcha(r *http.Request, provider, response string) (verified bool) {
	endpoint := "https://www.google.com/recaptcha/api/siteverify"
	if provider == "hcaptcha" {
		endpoint = "https://api.hcaptcha.com/siteverify"
	}
	form := url.Values{"secret": {fd.cfg.Server.Pages.Contact.CaptchaSecretKey}, "response": {response}}
	if fd.resolver != nil && !fd.resolver.IsOverlay(r) {
		form.Set("remoteip", fd.resolver.ResolveClientIP(r))
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			verified = false
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var result struct {
		Success bool `json:"success"`
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result) == nil && result.Success
}

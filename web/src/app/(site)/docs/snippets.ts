/**
 * Static API code examples for the public /docs page.
 * Kept in a dedicated module to keep page.tsx concise and ensure 100% alignment
 * with POST /v1/emails and GET /v1/emails/{id}.
 */

export const DIRECT_SEND_NODE = `const response = await fetch("https://zmail.im/v1/emails", {
  method: "POST",
  headers: {
    "Authorization": \`Bearer \${process.env.ZMAIL_API_KEY}\`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    from: "YourApp <noreply@yourdomain.com>",
    to: "user@example.com",
    subject: "Welcome to YourApp",
    html: "<p>Hello! Your account is ready.</p>",
    text: "Hello! Your account is ready.",
  }),
});

const result = await response.json();
console.log(response.status, result);`;

export const DIRECT_SEND_CURL = `curl -X POST "https://zmail.im/v1/emails" \\
  -H "Authorization: Bearer $ZMAIL_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "from": "YourApp <noreply@yourdomain.com>",
    "to": "user@example.com",
    "subject": "Welcome to YourApp",
    "html": "<p>Hello! Your account is ready.</p>",
    "text": "Hello! Your account is ready."
  }'`;

export const DIRECT_SEND_PYTHON = `import json
import os
import urllib.request

payload = {
    "from": "YourApp <noreply@yourdomain.com>",
    "to": "user@example.com",
    "subject": "Welcome to YourApp",
    "html": "<p>Hello! Your account is ready.</p>",
    "text": "Hello! Your account is ready.",
}

req = urllib.request.Request(
    "https://zmail.im/v1/emails",
    data=json.dumps(payload).encode("utf-8"),
    method="POST",
    headers={
        "Authorization": f"Bearer {os.environ['ZMAIL_API_KEY']}",
        "Content-Type": "application/json",
    },
)

with urllib.request.urlopen(req) as resp:
    print(resp.status, json.load(resp))`;

export const DIRECT_SEND_GO = `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	body, _ := json.Marshal(map[string]any{
		"from":    "YourApp <noreply@yourdomain.com>",
		"to":      "user@example.com",
		"subject": "Welcome to YourApp",
		"html":    "<p>Hello! Your account is ready.</p>",
		"text":    "Hello! Your account is ready.",
	})

	req, _ := http.NewRequest(http.MethodPost, "https://zmail.im/v1/emails", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("ZMAIL_API_KEY"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.StatusCode, string(data))
}`;

export const DIRECT_SEND_RESPONSE = `HTTP/1.1 202 Accepted
Location: /v1/emails/42
Content-Type: application/json

{
  "code": 0,
  "message": "success",
  "data": {
    "id": 42,
    "status": "queued",
    "from": "YourApp <noreply@yourdomain.com>",
    "to": "user@example.com",
    "subject": "Welcome to YourApp",
    "created_at": "2026-11-25T08:30:00Z"
  }
}`;

export const TEMPLATE_SEND_NODE = `const response = await fetch("https://zmail.im/v1/emails", {
  method: "POST",
  headers: {
    "Authorization": \`Bearer \${process.env.ZMAIL_API_KEY}\`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    template: "login_otp",
    to: "user@example.com",
    variables: {
      code: "849201",
      expires_in: "10",
    },
  }),
});`;

export const TEMPLATE_SEND_CURL = `curl -X POST "https://zmail.im/v1/emails" \\
  -H "Authorization: Bearer $ZMAIL_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "template": "login_otp",
    "to": "user@example.com",
    "variables": {
      "code": "849201",
      "expires_in": "10"
    }
  }'`;

export const TEMPLATE_SEND_PYTHON = `import json
import os
import urllib.request

payload = {
    "template": "login_otp",
    "to": "user@example.com",
    "variables": {
        "code": "849201",
        "expires_in": "10",
    },
}

req = urllib.request.Request(
    "https://zmail.im/v1/emails",
    data=json.dumps(payload).encode("utf-8"),
    method="POST",
    headers={
        "Authorization": f"Bearer {os.environ['ZMAIL_API_KEY']}",
        "Content-Type": "application/json",
    },
)

with urllib.request.urlopen(req) as resp:
    print(resp.status, json.load(resp))`;

export const TEMPLATE_SEND_GO = `body, _ := json.Marshal(map[string]any{
	"template": "login_otp",
	"to":       "user@example.com",
	"variables": map[string]string{
		"code":       "849201",
		"expires_in": "10",
	},
})

req, _ := http.NewRequest(http.MethodPost, "https://zmail.im/v1/emails", bytes.NewReader(body))
req.Header.Set("Authorization", "Bearer "+os.Getenv("ZMAIL_API_KEY"))
req.Header.Set("Content-Type", "application/json")
resp, err := http.DefaultClient.Do(req)`;

export const STATUS_QUERY_CURL = `curl "https://zmail.im/v1/emails/42" \\
  -H "Authorization: Bearer $ZMAIL_API_KEY"`;

export const STATUS_QUERY_RESPONSE = `{
  "code": 0,
  "message": "success",
  "data": {
    "id": 42,
    "status": "accepted",
    "to": "user@example.com",
    "subject": "Welcome to YourApp",
    "created_at": "2026-11-25T08:30:00Z",
    "sent_at": "2026-11-25T08:30:02Z"
  }
}`;

export const IDEMPOTENCY_CURL = `curl -X POST "https://zmail.im/v1/emails" \\
  -H "Authorization: Bearer $ZMAIL_API_KEY" \\
  -H "Idempotency-Key: order-1042-receipt" \\
  -H "Content-Type: application/json" \\
  -d '{
    "from": "Billing <billing@yourdomain.com>",
    "to": "user@example.com",
    "subject": "Receipt #1042",
    "text": "Thank you for your payment."
  }'`;

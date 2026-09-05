// Independent management API signer. This file does not modify or import Frozen Core.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type vector struct {
	NodeId, Timestamp, Nonce, Method, PathAndQuery, Body, PublicKey, Signature string
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: sign <vectors.json>")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	var vectors []vector
	for i, data := range [][3]string{
		{"GET", "/api/v1/node/config?node_id=go-interop&x=a%2Fb&x=a+b", ""},
		{"POST", "/api/v1/nodes/go-interop/health", "{\"node_id\":\"go-interop\",\"current_sessions\":3,\"note\":\"Привет\"}\n"},
		{"GET", "/api/v1/revocation?since=0", ""},
	} {
		req, err := http.NewRequest(data[0], "https://control.example"+data[1], strings.NewReader(data[2]))
		if err != nil {
			panic(err)
		}
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			panic(err)
		}
		v := vector{NodeId: "go-interop", Timestamp: strconv.FormatInt(time.Now().Unix(), 10),
			Nonce: base64.RawURLEncoding.EncodeToString(nonce), Method: req.Method,
			PathAndQuery: req.URL.EscapedPath(), Body: data[2], PublicKey: base64.StdEncoding.EncodeToString(pub)}
		if req.URL.RawQuery != "" {
			v.PathAndQuery += "?" + req.URL.RawQuery
		}
		hash := sha256.Sum256([]byte(v.Body))
		message := strings.Join([]string{"nvp-node-req-v2", v.NodeId, v.Timestamp, v.Nonce, v.Method, v.PathAndQuery, hex.EncodeToString(hash[:])}, "|")
		v.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(message)))
		vectors = append(vectors, v)
		fmt.Printf("GO_SIGNED_VECTOR_%d=PASS\n", i+1)
	}
	encoded, err := json.Marshal(vectors)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[1], encoded, 0600); err != nil {
		panic(err)
	}
}

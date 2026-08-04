// Package health holds the liveness and readiness checks.
//
// It exists as its own package because two different HTTP servers have to answer them
// identically: the gRPC-Gateway mux, which is what actually listens in the deployed
// service, and the Gin router used for the account and transfer endpoints. A probe that
// only worked against one of them would report on a server the kubelet is not talking
// to.
//
// Liveness and readiness are deliberately different checks, because Kubernetes reacts
// to them differently.
//
// A failing liveness probe restarts the pod, so liveness must not depend on Postgres.
// If the database goes down, restarting every replica turns an outage into a crash loop
// and the pods come back no healthier than they left.
//
// A failing readiness probe only removes the pod from the Service's endpoints, which is
// exactly the right response to an unreachable database: stop sending it traffic, leave
// the process alone, let it rejoin when the dependency recovers.
package health

import (
	"context"
	"encoding/json"
	"net/http"
)

// Pinger is the part of the store the readiness check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

const (
	// StatusLive is reported when the process is up and serving.
	StatusLive = "ok"
	// StatusReady is reported when the process can also reach its database.
	StatusReady = "ready"
	// StatusUnavailable is reported when a dependency is unusable.
	StatusUnavailable = "unavailable"
)

type Response struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func write(w http.ResponseWriter, code int, rsp Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(rsp)
}

// Live reports that the process is up. Nothing else -- see the package comment.
func Live(w http.ResponseWriter, r *http.Request) {
	write(w, http.StatusOK, Response{Status: StatusLive})
}

// Ready reports whether this instance can serve requests, which means having a usable
// database connection. It answers 503 rather than 500: the pod is fine, its dependency
// is not, and Kubernetes should pull it out of the Service instead of restarting it.
func Ready(pinger Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pinger.Ping(r.Context()); err != nil {
			write(w, http.StatusServiceUnavailable, Response{
				Status: StatusUnavailable,
				Error:  err.Error(),
			})
			return
		}

		write(w, http.StatusOK, Response{Status: StatusReady})
	}
}

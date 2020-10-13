package gcs

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/api/option"
	google_http "google.golang.org/api/transport/http"
	ot_log "github.com/opentracing/opentracing-go/log"
)

var (
	gcsRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tempodb",
		Name:      "gcs_request_duration_seconds",
		Help:      "Time spent doing GCS requests.",

		// GCS latency seems to range from a few ms to a few secs and is
		// important.  So use 6 buckets from 5ms to 5s.
		Buckets: prometheus.ExponentialBuckets(0.005, 4, 6),
	}, []string{"operation", "status_code"})
)

type instrumentedTransport struct {
	observer prometheus.ObserverVec
	next     http.RoundTripper
}

func instrumentation(ctx context.Context, scope string) (option.ClientOption, error) {
	transport, err := google_http.NewTransport(ctx, http.DefaultTransport, option.WithScopes(scope))
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: instrumentedTransport{
			observer: gcsRequestDuration,
			next:     transport,
		},
	}
	return option.WithHTTPClient(client), nil
}

func (i instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()

	wireCtx, err := opentracing.GlobalTracer().Extract(
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(req.Header))
	span := opentracing.StartSpan(
		"gcs " + req.Method,
		ext.RPCServerOption(wireCtx))
	span.SetTag("URL", req.URL.String())
	defer span.Finish()

	resp, err := i.next.RoundTrip(req)
	if err == nil {
		i.observer.WithLabelValues(req.Method, strconv.Itoa(resp.StatusCode)).Observe(time.Since(start).Seconds())
	} else {
		span.LogFields(ot_log.Error(err))
	}
	return resp, err
}

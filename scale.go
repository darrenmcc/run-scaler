package scale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"cloud.google.com/go/compute/metadata"
	"github.com/go-kit/kit/endpoint"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/run/v1"
)

func NewHandler(min, max int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		err := Scale(context.Background(), min, max)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// NewEndpoint can be used as a go-kit endpoint in any Gizmo service.
func NewEndpoint(min, max int) endpoint.Endpoint {
	return func(ctx context.Context, _ interface{}) (interface{}, error) {
		return nil, Scale(ctx, min, max)
	}
}

func Scale(ctx context.Context, min, max int) error {
	httpClient, err := google.DefaultClient(ctx, run.CloudPlatformScope)
	if err != nil {
		return err
	}

	project, err := metadata.ProjectID()
	if err != nil {
		return err
	}

	runAdminURL := fmt.Sprintf(
		"https://us-central1-run.googleapis.com/apis/serving.knative.dev/v1/namespaces/%s/services/%s",
		project, os.Getenv("K_SERVICE"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, runAdminURL, nil)
	if err != nil {
		return err
	}
	svcResp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer svcResp.Body.Close()

	var svc run.Service
	err = json.NewDecoder(svcResp.Body).Decode(&svc)
	if err != nil {
		return err
	}

	// noop if new scaling values are same as current
	newMin := strconv.Itoa(min)
	newMax := strconv.Itoa(max)
	if svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/minScale"] == newMin &&
		svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"] == newMax {
		return nil
	}

	// BETA annotation required on top-level metadata for minScale setting
	svc.Metadata.Annotations["run.googleapis.com/launch-stage"] = "BETA"
	// zero out name so new revision name is generated, or else request will
	// fail because service with this name already exists
	svc.Spec.Template.Metadata.Name = ""
	svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/minScale"] = newMin
	svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"] = newMax

	b, err := json.Marshal(svc)
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPut, runAdminURL, bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	updateResp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer updateResp.Body.Close()

	if updateResp.StatusCode != http.StatusOK {
		return fmt.Errorf("Cloud Run API response code: %d", updateResp.StatusCode)
	}
	return nil
}

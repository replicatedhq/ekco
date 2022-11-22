package webhook

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/martian/log"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RookCephNS = "rook-ceph"
)

func (s *Server) rookPriority(c *gin.Context) {
	request := admissionv1.AdmissionReview{}
	response := admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{},
	}
	if request.Request != nil {
		response.Response.UID = request.Request.UID
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	_, _, err = codecs.UniversalDeserializer().Decode(body, nil, &request)
	if err != nil {
		s.log.Error(err)
		response.Response.Result = &metav1.Status{
			Message: err.Error(),
		}
	} else if request.Request != nil &&
		request.Request.Resource.Group == "apps" &&
		(request.Request.Resource.Resource == "deployments" || request.Request.Resource.Resource == "daemonsets") &&
		request.Request.Namespace == RookCephNS {

		log.Infof("Admission webhook mutating priority class for %s/%s", request.Request.Namespace, request.Request.Name)
		response.Response.Allowed = true
		pt := admissionv1.PatchTypeJSONPatch
		response.Response.PatchType = &pt
		patch := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/priorityClassName","value":"%s"}]`, s.rookPriorityClass)
		response.Response.Patch = []byte(patch)
	} else {
		log.Debugf("Admission webhook ignoring %s/%s", request.Request.Namespace, request.Request.Name)
		response.Response.Allowed = true
	}

	c.JSON(http.StatusOK, response)
}

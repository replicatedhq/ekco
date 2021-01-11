package webhook

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/martian/log"
	"k8s.io/api/admission/v1beta1"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

var scheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(scheme)

func init() {
	utilruntime.Must(admissionv1beta1.AddToScheme(scheme))
}

func (s *Server) rookPriority(c *gin.Context) {
	request := v1beta1.AdmissionReview{}
	response := v1beta1.AdmissionReview{
		Response: &admissionv1beta1.AdmissionResponse{},
	}
	if request.Request != nil {
		response.Response.UID = request.Request.UID
	}

	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
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
		request.Request.Namespace == "rook-ceph" {

		log.Infof("Admission webhook mutating priority class for %s/%s", request.Request.Namespace, request.Request.Name)
		response.Response.Allowed = true
		pt := v1beta1.PatchTypeJSONPatch
		response.Response.PatchType = &pt
		patch := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/priorityClassName","value":"%s"}]`, s.priorityClass)
		response.Response.Patch = []byte(patch)
	} else {
		log.Debugf("Admission webhook ignoring %s/%s", request.Request.Namespace, request.Request.Name)
		response.Response.Allowed = true
	}

	c.JSON(http.StatusOK, response)
}

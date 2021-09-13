package webhook

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/martian/log"
	"k8s.io/api/admission/v1beta1"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) overridePodImages(c *gin.Context) {
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
	} else if request.Request != nil && request.Request.Resource.Resource == "pods" {

		log.Debugf("Admission webhook checking for image overrides for pod %s/%s", request.Request.Namespace, request.Request.Name)
		response.Response.Allowed = true

		raw := request.Request.Object.Raw
		pod := corev1.Pod{}
		deserializer := codecs.UniversalDeserializer()
		if _, _, err := deserializer.Decode(raw, nil, &pod); err != nil {
			s.log.Error(err)
			response.Response.Result = &metav1.Status{
				Message: err.Error(),
			}
			return
		}

		var patches []string
		for i, container := range pod.Spec.InitContainers {
			newImage, ok := s.podImageOverrides[container.Image]
			if !ok {
				continue
			}
			patch := fmt.Sprintf(`{"op":"replace","path":"/spec/initContainers/%d/image","value":"%s"}`, i, newImage)
			patches = append(patches, patch)
			log.Infof("Overriding image %q with %q in pod %s/%s", container.Image, newImage, pod.Namespace, pod.Name)
		}

		for i, container := range pod.Spec.Containers {
			newImage, ok := s.podImageOverrides[container.Image]
			if !ok {
				continue
			}
			patch := fmt.Sprintf(`{"op":"replace","path":"/spec/containers/%d/image","value":"%s"}`, i, newImage)
			patches = append(patches, patch)
			log.Infof("Overriding image %q with %q in pod %s/%s", container.Image, newImage, pod.Namespace, pod.Name)
		}

		if len(patches) == 0 {
			log.Debugf("Admission webhook found no image overrides for pod %s/%s", request.Request.Namespace, request.Request.Name)
		} else {
			pt := v1beta1.PatchTypeJSONPatch
			response.Response.PatchType = &pt
			patch := fmt.Sprintf(`[%s]`, strings.Join(patches, ","))
			response.Response.Patch = []byte(patch)
		}
	} else {
		log.Debugf("Admission webhook ignoring %s/%s", request.Request.Namespace, request.Request.Name)
		response.Response.Allowed = true
	}

	c.JSON(http.StatusOK, response)
}

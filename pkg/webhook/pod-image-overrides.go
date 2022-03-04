package webhook

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) overridePodImages(c *gin.Context) {
	request := admissionv1.AdmissionReview{}
	response := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &admissionv1.AdmissionResponse{},
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
		c.JSON(http.StatusOK, response)
		return
	}

	response.Response.Allowed = true
	if request.Request != nil {
		response.Response.UID = request.Request.UID
	}

	if request.Request != nil && request.Request.Resource.Resource == "pods" {
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

		s.log.Debugf("Admission webhook checking for image overrides for pod %s/%s", pod.Namespace, pod.Name)

		var patches []string
		for i, container := range pod.Spec.InitContainers {
			newImage, ok := s.podImageOverrides[container.Image]
			if !ok {
				continue
			}
			patch := fmt.Sprintf(`{"op":"replace","path":"/spec/initContainers/%d/image","value":"%s"}`, i, newImage)
			patches = append(patches, patch)
			s.log.Infof("Overriding image %q with %q in pod %s/%s", container.Image, newImage, pod.Namespace, pod.Name)
		}

		for i, container := range pod.Spec.Containers {
			newImage, ok := s.podImageOverrides[container.Image]
			if !ok {
				continue
			}
			patch := fmt.Sprintf(`{"op":"replace","path":"/spec/containers/%d/image","value":"%s"}`, i, newImage)
			patches = append(patches, patch)
			s.log.Infof("Overriding image %q with %q in pod %s/%s", container.Image, newImage, pod.Namespace, pod.Name)
		}

		if len(patches) == 0 {
			s.log.Debugf("Admission webhook found no image overrides for pod %s/%s", pod.Namespace, pod.Name)
		} else {
			pt := admissionv1.PatchTypeJSONPatch
			response.Response.PatchType = &pt
			patch := fmt.Sprintf(`[%s]`, strings.Join(patches, ","))
			response.Response.Patch = []byte(patch)
		}
	} else {
		s.log.Debugf("Admission webhook ignoring %s/%s", request.Request.Namespace, request.Request.Name)
	}

	c.JSON(http.StatusOK, response)
}

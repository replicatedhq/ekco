package webhook

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	"go.uber.org/zap"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	certutil "k8s.io/client-go/util/cert"
)

const WebhookServiceName = "ekco"
const RookPriorityAdmissionWebhookName = "rook-priority.kurl.sh"
const PodImageOverridesAdmissionWebhookName = "pod-image-overrides.kurl.sh"

type Server struct {
	tls               *tls.Config
	log               *zap.SugaredLogger
	rookPriorityClass string
	podImageOverrides map[string]string
}

func NewServer(client kubernetes.Interface, namespace string, rookPriorityClass string, podImageOverrides map[string]string, log *zap.SugaredLogger) (*Server, error) {
	server := &Server{
		log:               log,
		rookPriorityClass: rookPriorityClass,
		podImageOverrides: podImageOverrides,
	}

	// Ensure ekco service exists
	_, err := client.CoreV1().Services(namespace).Get(context.TODO(), WebhookServiceName, metav1.GetOptions{})
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return nil, errors.Wrapf(err, "get %s service", WebhookServiceName)
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      WebhookServiceName,
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "ekc-operator"},
				Ports: []corev1.ServicePort{
					{
						Port:       443,
						TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: int32(443)},
					},
				},
			},
		}
		_, err := client.CoreV1().Services(namespace).Create(context.TODO(), service, metav1.CreateOptions{})
		if err != nil {
			return nil, errors.Wrapf(err, "create %s service", WebhookServiceName)
		}
	}

	// Generate self-signed cert with CA for server
	host := fmt.Sprintf("%s.%s.svc", WebhookServiceName, namespace)
	certPEM, keyPEM, err := certutil.GenerateSelfSignedCertKey(host, nil, nil)
	if err != nil {
		return nil, errors.Wrap(err, "generate self-signed cert for webhook")
	}
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, errors.Wrap(err, "load self-signed tls certificate")
	}
	server.tls = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	// Configure the rook priority class webhook
	if rookPriorityClass != "" {
		// Ensure the node-critical priority class exists
		_, err := client.SchedulingV1().PriorityClasses().Get(context.TODO(), rookPriorityClass, metav1.GetOptions{})
		if err != nil {
			if !util.IsNotFoundErr(err) {
				return nil, errors.Wrapf(err, "get priorityclass %s", rookPriorityClass)
			}
			pc := &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: rookPriorityClass,
				},
				Value:       1000000000,
				Description: "Used for pods that provide critical services to their node",
			}
			_, err = client.SchedulingV1().PriorityClasses().Create(context.TODO(), pc, metav1.CreateOptions{})
			if err != nil {
				return nil, errors.Wrapf(err, "create priorityclass %s", rookPriorityClass)
			}
		}

		// Ensure the mutating webhook config exists
		port := int32(443)
		path := "/rook-priority"
		equivalent := admissionregistrationv1.Equivalent
		ignore := admissionregistrationv1.Ignore
		none := admissionregistrationv1.SideEffectClassNone
		timeout := int32(10)

		webhook := admissionregistrationv1.MutatingWebhook{
			Name:                    RookPriorityAdmissionWebhookName,
			MatchPolicy:             &equivalent,
			FailurePolicy:           &ignore,
			SideEffects:             &none,
			TimeoutSeconds:          &timeout,
			AdmissionReviewVersions: []string{"v1"},
			NamespaceSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      RookPriorityAdmissionWebhookName,
						Operator: metav1.LabelSelectorOpExists,
					},
				},
			},
			Rules: []admissionregistrationv1.RuleWithOperations{
				{
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{"apps"},
						APIVersions: []string{"v1"},
						Resources:   []string{"deployments", "daemonsets"},
					},
					Operations: []admissionregistrationv1.OperationType{
						admissionregistrationv1.Create,
						admissionregistrationv1.Update,
					},
				},
			},
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Namespace: namespace,
					Name:      WebhookServiceName,
					Path:      &path,
					Port:      &port,
				},
				CABundle: certPEM,
			},
		}

		hook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), RookPriorityAdmissionWebhookName, metav1.GetOptions{})
		if err != nil {
			if !util.IsNotFoundErr(err) {
				return nil, errors.Wrap(err, "get rook-priority mutating admission webhook")
			}
			hook := &admissionregistrationv1.MutatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: RookPriorityAdmissionWebhookName,
				},
				Webhooks: []admissionregistrationv1.MutatingWebhook{webhook},
			}
			if _, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.TODO(), hook, metav1.CreateOptions{}); err != nil {
				return nil, errors.Wrap(err, "create rook-priority mutating admission webhook")
			}
		} else {
			hook.Webhooks = []admissionregistrationv1.MutatingWebhook{webhook}
			if _, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(), hook, metav1.UpdateOptions{}); err != nil {
				return nil, errors.Wrap(err, "update rook-priority mutating admission webhook")
			}
		}
	}

	if len(podImageOverrides) > 0 {
		port := int32(443)
		path := "/pod-image-overrides"
		equivalent := admissionregistrationv1.Equivalent
		ignore := admissionregistrationv1.Ignore
		none := admissionregistrationv1.SideEffectClassNone
		timeout := int32(10)

		webhook := admissionregistrationv1.MutatingWebhook{
			Name:                    PodImageOverridesAdmissionWebhookName,
			MatchPolicy:             &equivalent,
			FailurePolicy:           &ignore,
			SideEffects:             &none,
			TimeoutSeconds:          &timeout,
			AdmissionReviewVersions: []string{"v1"},
			Rules: []admissionregistrationv1.RuleWithOperations{
				{
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{""},
						APIVersions: []string{"v1"},
						Resources:   []string{"pods"},
					},
					Operations: []admissionregistrationv1.OperationType{
						admissionregistrationv1.Create,
						admissionregistrationv1.Update,
					},
				},
			},
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Namespace: namespace,
					Name:      WebhookServiceName,
					Path:      &path,
					Port:      &port,
				},
				CABundle: certPEM,
			},
		}

		hook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), PodImageOverridesAdmissionWebhookName, metav1.GetOptions{})
		if err != nil {
			if !util.IsNotFoundErr(err) {
				return nil, errors.Wrap(err, "get pod-image-overrides mutating admission webhook")
			}
			hook := &admissionregistrationv1.MutatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: PodImageOverridesAdmissionWebhookName,
				},
				Webhooks: []admissionregistrationv1.MutatingWebhook{webhook},
			}
			if _, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.TODO(), hook, metav1.CreateOptions{}); err != nil {
				return nil, errors.Wrap(err, "create pod-image-overrides mutating admission webhook")
			}
		} else {
			hook.Webhooks = []admissionregistrationv1.MutatingWebhook{webhook}
			if _, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(), hook, metav1.UpdateOptions{}); err != nil {
				return nil, errors.Wrap(err, "update pod-image-overrides mutating admission webhook")
			}
		}
	}

	return server, nil
}

func (s *Server) Run() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	if s.rookPriorityClass != "" {
		r.POST("/rook-priority", s.rookPriority)
	}
	if len(s.podImageOverrides) > 0 {
		r.POST("/pod-image-overrides", s.overridePodImages)
	}

	tlsserver := &http.Server{
		Addr:      ":443",
		TLSConfig: s.tls,
		Handler:   r,
	}
	fmt.Printf("Admission webhook server listening on %s\n", tlsserver.Addr)
	err := tlsserver.ListenAndServeTLS("", "")
	log.Panic(err)
}

// RemoveRookPriority removes a legacy webhook used with Rook 1.0.4 and KURL to adjust the priority class.
func RemoveRookPriority(client kubernetes.Interface) error {
	err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(context.TODO(), RookPriorityAdmissionWebhookName, metav1.DeleteOptions{})
	if err != nil && !util.IsNotFoundErr(err) {
		return errors.Wrapf(err, "failed to delete mutating webhook config %s", RookPriorityAdmissionWebhookName)
	}
	return nil
}

func RemovePodImageOverrides(client kubernetes.Interface) error {
	err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(context.TODO(), PodImageOverridesAdmissionWebhookName, metav1.DeleteOptions{})
	if err != nil && !util.IsNotFoundErr(err) {
		return errors.Wrapf(err, "failed to delete mutating webhook config %s", PodImageOverridesAdmissionWebhookName)
	}
	return nil
}

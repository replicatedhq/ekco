package webhook

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	"go.uber.org/zap"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	certutil "k8s.io/client-go/util/cert"
)

const WebhookServiceName = "ekco"
const AdmissionWebhookName = "rook-priority.kurl.sh"

type Server struct {
	tls           *tls.Config
	log           *zap.SugaredLogger
	priorityClass string
}

func NewServer(client kubernetes.Interface, namespace string, priorityClass string, log *zap.SugaredLogger) (*Server, error) {
	server := &Server{
		log:           log,
		priorityClass: priorityClass,
	}

	// Ensure the node-critical priority class exists
	_, err := client.SchedulingV1().PriorityClasses().Get(priorityClass, metav1.GetOptions{})
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return nil, errors.Wrapf(err, "get priorityclass %s", priorityClass)
		}
		pc := &schedulingv1.PriorityClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: priorityClass,
			},
			Value:       1000000000,
			Description: "Used for pods that provide critical services to their node",
		}
		_, err = client.SchedulingV1().PriorityClasses().Create(pc)
		if err != nil {
			return nil, errors.Wrapf(err, "create priorityclass %s", priorityClass)
		}
	}

	// Ensure ekco service exists
	_, err = client.CoreV1().Services(namespace).Get(WebhookServiceName, metav1.GetOptions{})
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
		_, err := client.CoreV1().Services(namespace).Create(service)
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

	// Configure the webhook
	port := int32(443)
	path := "/rook-priority"
	equivalent := admissionregistrationv1beta1.Equivalent
	ignore := admissionregistrationv1beta1.Ignore
	none := admissionregistrationv1beta1.SideEffectClassNone
	timeout := int32(10)

	webhook := admissionregistrationv1beta1.MutatingWebhook{
		Name:           AdmissionWebhookName,
		MatchPolicy:    &equivalent,
		FailurePolicy:  &ignore,
		SideEffects:    &none,
		TimeoutSeconds: &timeout,
		NamespaceSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      AdmissionWebhookName,
					Operator: metav1.LabelSelectorOpExists,
				},
			},
		},
		Rules: []admissionregistrationv1beta1.RuleWithOperations{
			{
				Rule: admissionregistrationv1beta1.Rule{
					APIGroups:   []string{"apps"},
					APIVersions: []string{"v1"},
					Resources:   []string{"deployments", "daemonsets"},
				},
				Operations: []admissionregistrationv1beta1.OperationType{
					admissionregistrationv1beta1.Create,
					admissionregistrationv1beta1.Update,
				},
			},
		},
		ClientConfig: admissionregistrationv1beta1.WebhookClientConfig{
			Service: &admissionregistrationv1beta1.ServiceReference{
				Namespace: namespace,
				Name:      WebhookServiceName,
				Path:      &path,
				Port:      &port,
			},
			CABundle: certPEM,
		},
	}

	hook, err := client.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Get(AdmissionWebhookName, metav1.GetOptions{})
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return nil, errors.Wrap(err, "get rook-priority mutating admission webhook")
		}
		hook := &admissionregistrationv1beta1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: AdmissionWebhookName,
			},
			Webhooks: []admissionregistrationv1beta1.MutatingWebhook{webhook},
		}
		if _, err := client.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Create(hook); err != nil {
			return nil, errors.Wrap(err, "create rook-priority mutating admission webhook")
		}
	} else {
		hook.Webhooks = []admissionregistrationv1beta1.MutatingWebhook{webhook}
		if _, err := client.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Update(hook); err != nil {
			return nil, errors.Wrap(err, "update rook-priority mutating admission webhook")
		}
	}

	return server, nil
}

func (s *Server) Run() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.POST("/rook-priority", s.rookPriority)

	tlsserver := &http.Server{
		Addr:      ":443",
		TLSConfig: s.tls,
		Handler:   r,
	}
	fmt.Printf("Admission webhook server listening on %s\n", tlsserver.Addr)
	err := tlsserver.ListenAndServeTLS("", "")
	log.Panic(err)
}

func Remove(client kubernetes.Interface) error {
	err := client.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Delete(AdmissionWebhookName, &metav1.DeleteOptions{})
	if err != nil && !util.IsNotFoundErr(err) {
		return errors.Wrapf(err, "failed to delete mutating webhook config %s", AdmissionWebhookName)
	}
	return nil
}

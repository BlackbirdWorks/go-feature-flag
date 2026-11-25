package k8sretriever

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

var (
	errK8sClientIsNil = errors.New("k8s client is nil after initialization")
	errKey            = errors.New("key")
)

// Retriever is a configuration struct for a Kubernetes retriever.
type Retriever struct {
	ClientConfig restclient.Config
	client       kubernetes.Interface
	// newClient creates the client from ClientConfig (kubernetes.NewForConfig when nil). The tests set it.
	newClient     func(*restclient.Config) (kubernetes.Interface, error)
	Namespace     string
	ConfigMapName string
	Key           string
}

// Retrieve is the function in charge of fetching the flag configuration.
func (s *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	clientErr := s.ensureClient()
	if clientErr != nil {
		return nil, fmt.Errorf("unable to create client, error: %w", clientErr)
	}

	if s.client == nil {
		return nil, errK8sClientIsNil
	}

	configMap, err := s.client.CoreV1().
		ConfigMaps(s.Namespace).
		Get(ctx, s.ConfigMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf(
			"unable to read from config map %s.%s, error: %w", s.ConfigMapName, s.Namespace, err,
		)
	}

	content, ok := configMap.Data[s.Key]
	if !ok {
		return nil, fmt.Errorf("%w %s not existing in config map %s.%s", errKey,
			s.Key,
			s.ConfigMapName,
			s.Namespace,
		)
	}

	return []byte(content), nil
}

// ensureClient creates the Kubernetes client of the retriever when it is not already set.
func (s *Retriever) ensureClient() error {
	if s.client != nil {
		return nil
	}

	if s.newClient != nil {
		client, err := s.newClient(&s.ClientConfig)
		if err != nil {
			return err
		}

		s.client = client

		return nil
	}

	client, err := kubernetes.NewForConfig(&s.ClientConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}

	s.client = client

	return nil
}

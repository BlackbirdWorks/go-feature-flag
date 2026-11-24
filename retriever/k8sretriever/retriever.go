package k8sretriever

import (
	"context"
	"errors"
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

// Retriever is a configuration struct for a Kubernetes retriever.
type Retriever struct {
	Namespace     string
	ConfigMapName string
	Key           string
	ClientConfig  restclient.Config
	client        kubernetes.Interface
	// newClient creates the client from ClientConfig (kubernetes.NewForConfig when nil). The tests set it.
	newClient func(*restclient.Config) (kubernetes.Interface, error)
}

// createClient creates the Kubernetes client of the retriever.
func (s *Retriever) createClient() (kubernetes.Interface, error) {
	if s.newClient != nil {
		return s.newClient(&s.ClientConfig)
	}

	return kubernetes.NewForConfig(&s.ClientConfig)
}

// Retrieve is the function in charge of fetching the flag configuration.
func (s *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	if s.client == nil {
		client, clientErr := s.createClient()
		if clientErr != nil {
			return nil, fmt.Errorf("unable to create client, error: %w", clientErr)
		}
		s.client = client
	}

	if s.client == nil {
		return nil, errors.New("k8s client is nil after initialization")
	}

	configMap, err := s.client.CoreV1().
		ConfigMaps(s.Namespace).
		Get(ctx, s.ConfigMapName, v1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf(
			"unable to read from config map %s.%s, error: %w", s.ConfigMapName, s.Namespace, err,
		)
	}
	content, ok := configMap.Data[s.Key]
	if !ok {
		return nil, fmt.Errorf(
			"key %s not existing in config map %s.%s",
			s.Key,
			s.ConfigMapName,
			s.Namespace,
		)
	}

	return []byte(content), nil
}

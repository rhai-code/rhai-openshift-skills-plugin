package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type UserInfo struct {
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	IsAdmin  bool     `json:"is_admin"`
}

type contextKey string

const userContextKey contextKey = "user"

type cachedUser struct {
	info      *UserInfo
	expiresAt time.Time
}

var (
	authCache   sync.Map
	authClient  *kubernetes.Clientset
	authInitErr error
	authOnce    sync.Once
	devMode     = os.Getenv("DEV_MODE") == "true"
)

func initAuthClient() {
	authOnce.Do(func() {
		var config *rest.Config
		var err error

		tokenPath := os.Getenv("KUBE_SA_TOKEN_PATH")
		caPath := os.Getenv("KUBE_SA_CA_PATH")
		if tokenPath != "" && caPath != "" {
			host := os.Getenv("KUBERNETES_SERVICE_HOST")
			port := os.Getenv("KUBERNETES_SERVICE_PORT")
			if host == "" || port == "" {
				authInitErr = fmt.Errorf("KUBERNETES_SERVICE_HOST/PORT not set")
				log.Printf("Warning: auth client not available: %v", authInitErr)
				return
			}
			config = &rest.Config{
				Host:            "https://" + host + ":" + port,
				BearerTokenFile: tokenPath,
				TLSClientConfig: rest.TLSClientConfig{CAFile: caPath},
			}
		} else {
			config, err = rest.InClusterConfig()
			if err != nil {
				authInitErr = err
				log.Printf("Warning: auth client not available (RBAC won't work): %v", err)
				return
			}
		}
		authClient, err = kubernetes.NewForConfig(config)
		if err != nil {
			authInitErr = err
			log.Printf("Warning: auth client not available (RBAC won't work): %v", err)
		}
	})
}

func GetUser(r *http.Request) *UserInfo {
	if user, ok := r.Context().Value(userContextKey).(*UserInfo); ok {
		return user
	}
	return &UserInfo{Username: "", IsAdmin: false}
}

func AuthMiddleware(next http.Handler) http.Handler {
	initAuthClient()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health endpoint
		if strings.HasSuffix(r.URL.Path, "/health") {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			if authClient == nil && devMode {
				ctx := context.WithValue(r.Context(), userContextKey, &UserInfo{Username: "anonymous", IsAdmin: true})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			httpError(w, http.StatusUnauthorized, "authorization required")
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Check cache
		hash := sha256.Sum256([]byte(token))
		cacheKey := hex.EncodeToString(hash[:])

		if cached, ok := authCache.Load(cacheKey); ok {
			cu := cached.(*cachedUser)
			if time.Now().Before(cu.expiresAt) {
				ctx := context.WithValue(r.Context(), userContextKey, cu.info)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			authCache.Delete(cacheKey)
		}

		if authClient == nil {
			if devMode {
				ctx := context.WithValue(r.Context(), userContextKey, &UserInfo{Username: "anonymous", IsAdmin: true})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			httpError(w, http.StatusServiceUnavailable, "authentication service unavailable")
			return
		}

		// TokenReview
		tr, err := authClient.AuthenticationV1().TokenReviews().Create(r.Context(), &authenticationv1.TokenReview{
			Spec: authenticationv1.TokenReviewSpec{Token: token},
		}, metav1.CreateOptions{})
		if err != nil || !tr.Status.Authenticated {
			httpError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		username := tr.Status.User.Username
		groups := tr.Status.User.Groups

		// SubjectAccessReview for admin check
		sar, err := authClient.AuthorizationV1().SubjectAccessReviews().Create(r.Context(), &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User:   username,
				Groups: groups,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group:    "skills.openshift.io",
					Resource: "plugins",
					Verb:     "admin",
				},
			},
		}, metav1.CreateOptions{})
		isAdmin := err == nil && sar.Status.Allowed

		user := &UserInfo{
			Username: username,
			Groups:   groups,
			IsAdmin:  isAdmin,
		}

		// Cache for 60 seconds
		authCache.Store(cacheKey, &cachedUser{
			info:      user,
			expiresAt: time.Now().Add(60 * time.Second),
		})

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func MeHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	jsonResponse(w, user)
}

// authorizeResource returns true if the user is the owner or an admin.
// If not authorized, it writes a 403 response and returns false.
func authorizeResource(w http.ResponseWriter, user *UserInfo, resourceOwner string) bool {
	if user.IsAdmin || user.Username == resourceOwner {
		return true
	}
	httpError(w, http.StatusForbidden, "access denied")
	return false
}

// CheckUserNamespaceAccess verifies that the user has permissions to create and
// exec into pods in the target namespace via SubjectAccessReview. This prevents
// privilege escalation where a user schedules a task in a namespace they don't
// have access to, relying on the plugin SA to create the pod on their behalf.
func CheckUserNamespaceAccess(user *UserInfo, namespace string) (allowed bool, missing []string) {
	initAuthClient()
	if authClient == nil {
		return true, nil
	}

	ctx := context.Background()
	checks := []struct {
		resource    string
		subresource string
		verb        string
		label       string
	}{
		{"pods", "", "create", "create pods"},
		{"pods", "exec", "create", "exec into pods"},
		{"pods", "", "delete", "delete pods"},
	}

	for _, c := range checks {
		sar, err := authClient.AuthorizationV1().SubjectAccessReviews().Create(ctx,
			&authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User:   user.Username,
					Groups: user.Groups,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Namespace:   namespace,
						Verb:        c.verb,
						Group:       "",
						Resource:    c.resource,
						Subresource: c.subresource,
					},
				},
			}, metav1.CreateOptions{})
		if err != nil || !sar.Status.Allowed {
			missing = append(missing, c.label)
		}
	}

	return len(missing) == 0, missing
}

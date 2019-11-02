/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"strings"

	execv1 "exec/api/v1"
	"os/exec"
	"regexp"

	"github.com/go-logr/logr"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podLister struct {
	indexer cache.Indexer
}

// ExecutorReconciler reconciles a Executor object
type ExecutorReconciler struct {
	client.Client
	Log logr.Logger
}

func ignoreNotFound(err error) error {
	if apierrs.IsNotFound(err) {
		return nil
	}
	return err
}

// +kubebuilder:rbac:groups=exec.chocolate-chip-stack.stackathon,resources=executors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=exec.chocolate-chip-stack.stackathon,resources=executors/status,verbs=get;update;patch

// Fundamentally, what this does is allow a batch apply of a command through all instances of a container
// with a given name. This is a function that is not provided as a service within kubernetes itself,
// as evidenced by https://github.com/kubernetes/kubernetes/issues/8876, and would improve quality-of-life immensely
// for administrators attempting to get batch internal status on pods.
func (r *ExecutorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("executor", req.NamespacedName)
	var executor execv1.Executor
	if err := r.Get(ctx, req.NamespacedName, &executor); err != nil {
		log.Error(err, "unable to fetch CronJob")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, ignoreNotFound(err)
	}
	podsCmd := exec.Command("kubectl", "get", "pods", "--namespace", fmt.Sprintf("%s", req.Namespace), "-o", "name")
	stdout, err := podsCmd.StdoutPipe()

	if err != nil {
		return ctrl.Result{}, nil
	}

	// This is the list of containers in the relevant namespace
	slurp, _ := ioutil.ReadAll(stdout)
	containersRaw := regexp.MustCompile(`/\s+/`).Split(string(slurp), -1)
	var finalOut string
	for _, container := range containersRaw {
		if strings.Contains(container, executor.Spec.ContainerName) {
			cmd := exec.Command("kubctl", "exec", "-t", container, executor.Spec.Command)
			stdout, _ := cmd.StdoutPipe()
			output, _ := ioutil.ReadAll(stdout)
			finalOut += fmt.Sprintf("Container: %s\noutput: %s\n", container, string(output))
		}
	}

	executor.Status.Output = finalOut
	if err := r.Status().Update(ctx, &executor); err != nil {
		log.Error(err, "Unable to update executor status")
	}

	// For each container, filter
	return ctrl.Result{}, nil
}

func (r *ExecutorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&execv1.Executor{}).
		Complete(r)
}

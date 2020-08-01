package helpers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/cluster-logging-operator/pkg/k8shandler"
	"github.com/openshift/cluster-logging-operator/pkg/logger"
	"github.com/openshift/cluster-logging-operator/pkg/utils"
)

type fluentReceiverLogStore struct {
	deployment     *apps.Deployment
	tc             *E2ETestFramework
	pipelineSecret *corev1.Secret
}

const (
	receiverName             = "fluent-receiver"
	secureFluentConfTemplate = `
<system>
	log_level info
</system>
<source>
  @type forward
  <transport tls>
	  ca_cert_path /etc/fluentd/secrets/ca-bundle.crt
	  ca_private_key_path /etc/fluentd/secrets/ca.key
  </transport>
  <security>
	shared_key fluent-receiver
	self_hostname fluent-receiver
  </security>
</source>
<match *_default_** **_kube-*_** **_openshift-*_** **_openshift_** journal.** system.var.log**>
  @type file
  append true
  path /tmp/infra.logs
  symlink_path /tmp/infra-logs
</match>
<match kubernetes.**>
  @type file
  append true
  path /tmp/app.logs
  symlink_path /tmp/app-logs
  </match>
  <match linux-audit.log** k8s-audit.log** openshift-audit.log**>
  @type file
  append true
  path /tmp/audit.logs
  symlink_path /tmp/audit-logs
</match>
<match **>
	@type stdout
</match>
	`
	unsecureFluentConf = `
<system>
	log_level warn
</system>
<source>
  @type forward
</source>
<match *_default_** **_kube-*_** **_openshift-*_** **_openshift_** journal.** system.var.log**>
  @type file
  append true
  path /tmp/infra.logs
  symlink_path /tmp/infra-logs
  </match>
  <match kubernetes.**>
  @type file
  append true
  path /tmp/app.logs
  symlink_path /tmp/app-logs
  </match>
  <match linux-audit.log** k8s-audit.log** openshift-audit.log**>
  @type file
  append true
  path /tmp/audit.logs
  symlink_path /tmp/audit-logs
</match>
<match **>
	@type stdout
</match>
	`
)

func (fluent *fluentReceiverLogStore) hasLogs(file string, timeToWait time.Duration) (bool, error) {
	options := metav1.ListOptions{
		LabelSelector: "component=fluent-receiver",
	}
	pods, err := fluent.tc.KubeClient.CoreV1().Pods(OpenshiftLoggingNS).List(context.TODO(), options)
	if err != nil {
		return false, err
	}
	if len(pods.Items) == 0 {
		return false, errors.New("No pods found for fluent receiver")
	}
	logger.Debugf("Pod %s", pods.Items[0].Name)
	cmd := fmt.Sprintf("ls %s | wc -l", file)

	err = wait.Poll(defaultRetryInterval, timeToWait, func() (done bool, err error) {
		output, err := fluent.tc.PodExec(OpenshiftLoggingNS, pods.Items[0].Name, "fluent-receiver", []string{"bash", "-c", cmd})
		if err != nil {
			return false, err
		}
		value, err := strconv.Atoi(strings.TrimSpace(output))
		if err != nil {
			logger.Debugf("Error parsing output: %s", output)
			return false, nil
		}
		return value > 0, nil
	})
	if err == wait.ErrWaitTimeout {
		return false, err
	}
	return true, err
}

func (fluent *fluentReceiverLogStore) logs(file string, timeToWait time.Duration) (string, error) {
	options := metav1.ListOptions{
		LabelSelector: "component=fluent-receiver",
	}
	pods, err := fluent.tc.KubeClient.CoreV1().Pods(OpenshiftLoggingNS).List(context.TODO(), options)
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", errors.New("No pods found for fluent receiver")
	}
	logger.Debugf("Pod %s", pods.Items[0].Name)
	cmd := fmt.Sprintf("cat %s | awk -F '\t' '{print $3}'| head -n 1", file)
	result := ""
	err = wait.Poll(defaultRetryInterval, timeToWait, func() (done bool, err error) {
		if result, err = fluent.tc.PodExec(OpenshiftLoggingNS, pods.Items[0].Name, "fluent-receiver", []string{"bash", "-c", cmd}); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == wait.ErrWaitTimeout {
		return "", err
	}
	return result, nil
}

func (fluent *fluentReceiverLogStore) ApplicationLogs(timeToWait time.Duration) (logs, error) {
	fl, err := fluent.logs("/tmp/app-logs", timeToWait)
	if err != nil {
		return nil, err
	}
	out := "[" + strings.TrimRight(strings.Replace(fl, "\n", ",", -1), ",") + "]"
	return ParseLogs(out)
}

func (fluent fluentReceiverLogStore) HasInfraStructureLogs(timeToWait time.Duration) (bool, error) {
	return fluent.hasLogs("/tmp/infra.logs", timeToWait)
}
func (fluent *fluentReceiverLogStore) HasApplicationLogs(timeToWait time.Duration) (bool, error) {
	return fluent.hasLogs("/tmp/app.logs", timeToWait)
}

func (fluent *fluentReceiverLogStore) HasAuditLogs(timeToWait time.Duration) (bool, error) {
	return fluent.hasLogs("/tmp/audit.logs", timeToWait)
}

func (fluent *fluentReceiverLogStore) GrepLogs(expr string, timeToWait time.Duration) (string, error) {
	return "Not Found", fmt.Errorf("Not implemented")
}

func (fluent *fluentReceiverLogStore) RetrieveLogs() (map[string]string, error) {
	result := map[string]string{
		"infra": "",
		"audit": "",
		"app":   "",
	}
	var err error
	for key := range result {
		var s string
		s, err = fluent.logs(fmt.Sprintf("/tmp/%s.logs", key), 30*time.Second)
		if err != nil {
			continue
		}
		result[key] = s
	}
	return result, err
}

func (fluent *fluentReceiverLogStore) ClusterLocalEndpoint() string {
	panic("Not implemented")
}

func (tc *E2ETestFramework) createServiceAccount() (serviceAccount *corev1.ServiceAccount, err error) {
	opts := metav1.CreateOptions{}
	serviceAccount = k8shandler.NewServiceAccount("fluent-receiver", OpenshiftLoggingNS)
	if serviceAccount, err = tc.KubeClient.CoreV1().ServiceAccounts(OpenshiftLoggingNS).Create(context.TODO(), serviceAccount, opts); err != nil {
		return nil, err
	}
	tc.AddCleanup(func() error {
		opts := metav1.DeleteOptions{}
		return tc.KubeClient.CoreV1().ServiceAccounts(OpenshiftLoggingNS).Delete(context.TODO(), serviceAccount.Name, opts)
	})
	return serviceAccount, nil
}

func (tc *E2ETestFramework) createRbac(name string) (err error) {
	opts := metav1.CreateOptions{}
	saRole := k8shandler.NewRole(
		name,
		OpenshiftLoggingNS,
		k8shandler.NewPolicyRules(
			k8shandler.NewPolicyRule(
				[]string{"security.openshift.io"},
				[]string{"securitycontextconstraints"},
				[]string{"privileged"},
				[]string{"use"},
			),
		),
	)
	if _, err = tc.KubeClient.RbacV1().Roles(OpenshiftLoggingNS).Create(context.TODO(), saRole, opts); err != nil {
		return err
	}
	tc.AddCleanup(func() error {
		opts := metav1.DeleteOptions{}
		return tc.KubeClient.RbacV1().Roles(OpenshiftLoggingNS).Delete(context.TODO(), name, opts)
	})
	subject := k8shandler.NewSubject(
		"ServiceAccount",
		name,
	)
	subject.APIGroup = ""
	roleBinding := k8shandler.NewRoleBinding(
		name,
		OpenshiftLoggingNS,
		saRole.Name,
		k8shandler.NewSubjects(
			subject,
		),
	)
	if _, err = tc.KubeClient.RbacV1().RoleBindings(OpenshiftLoggingNS).Create(context.TODO(), roleBinding, opts); err != nil {
		return err
	}
	tc.AddCleanup(func() error {
		opts := metav1.DeleteOptions{}
		return tc.KubeClient.RbacV1().RoleBindings(OpenshiftLoggingNS).Delete(context.TODO(), name, opts)
	})
	return nil
}

func (tc *E2ETestFramework) DeployFluentdReceiver(rootDir string, secure bool) (deployment *apps.Deployment, err error) {
	logStore := &fluentReceiverLogStore{
		tc: tc,
	}
	serviceAccount, err := tc.createServiceAccount()
	if err != nil {
		return nil, err
	}
	if err := tc.createRbac(receiverName); err != nil {
		return nil, err
	}
	container := corev1.Container{
		Name:            receiverName,
		Image:           "quay.io/openshift/origin-logging-fluentd:latest",
		ImagePullPolicy: corev1.PullAlways,
		Args:            []string{"fluentd", "-c", "/fluentd/etc/fluent.conf"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "config",
				ReadOnly:  true,
				MountPath: "/fluentd/etc",
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: utils.GetBool(true),
		},
	}
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		Volumes: []corev1.Volume{
			{
				Name: "config", VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: container.Name,
						},
					},
				},
			},
		},
		ServiceAccountName: serviceAccount.Name,
	}

	fluentConf := unsecureFluentConf
	if secure {
		fluentConf = secureFluentConfTemplate
		otherConf := map[string][]byte{
			"shared_key": []byte("my_shared_key"),
		}
		if logStore.pipelineSecret, err = tc.CreatePipelineSecret(rootDir, receiverName, receiverName, otherConf); err != nil {
			return nil, err
		}
		tc.AddCleanup(func() error {
			opts := metav1.DeleteOptions{}
			return tc.KubeClient.CoreV1().Secrets(OpenshiftLoggingNS).Delete(context.TODO(), receiverName, opts)
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "certs",
			ReadOnly:  true,
			MountPath: "/etc/fluentd/secrets",
		})
		podSpec.Containers = []corev1.Container{container}
		logStore.pipelineSecret.Data["shared_key"] = []byte("fluent-receiver")

		opts := metav1.UpdateOptions{}
		if logStore.pipelineSecret, err = tc.KubeClient.CoreV1().Secrets(OpenshiftLoggingNS).Update(context.TODO(), logStore.pipelineSecret, opts); err != nil {
			return nil, err
		}
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: "certs", VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: logStore.pipelineSecret.Name,
				},
			},
		})
	}

	opts := metav1.CreateOptions{}
	config := k8shandler.NewConfigMap(container.Name, OpenshiftLoggingNS, map[string]string{
		"fluent.conf": fluentConf,
	})
	config, err = tc.KubeClient.CoreV1().ConfigMaps(OpenshiftLoggingNS).Create(context.TODO(), config, opts)
	if err != nil {
		return nil, err
	}
	tc.AddCleanup(func() error {
		opts := metav1.DeleteOptions{}
		return tc.KubeClient.CoreV1().ConfigMaps(OpenshiftLoggingNS).Delete(context.TODO(), config.Name, opts)
	})

	dOpts := metav1.CreateOptions{}
	fluentDeployment := k8shandler.NewDeployment(
		container.Name,
		OpenshiftLoggingNS,
		container.Name,
		serviceAccount.Name,
		podSpec,
	)

	fluentDeployment, err = tc.KubeClient.AppsV1().Deployments(OpenshiftLoggingNS).Create(context.TODO(), fluentDeployment, dOpts)
	if err != nil {
		return nil, err
	}
	service := k8shandler.NewService(
		serviceAccount.Name,
		OpenshiftLoggingNS,
		serviceAccount.Name,
		[]corev1.ServicePort{
			{
				Port: 24224,
			},
		},
	)
	tc.AddCleanup(func() error {
		var zerograce int64
		opts := metav1.DeleteOptions{
			GracePeriodSeconds: &zerograce,
		}
		return tc.KubeClient.AppsV1().Deployments(OpenshiftLoggingNS).Delete(context.TODO(), fluentDeployment.Name, opts)
	})

	sOpts := metav1.CreateOptions{}
	service, err = tc.KubeClient.CoreV1().Services(OpenshiftLoggingNS).Create(context.TODO(), service, sOpts)
	if err != nil {
		return nil, err
	}
	tc.AddCleanup(func() error {
		opts := metav1.DeleteOptions{}
		return tc.KubeClient.CoreV1().Services(OpenshiftLoggingNS).Delete(context.TODO(), service.Name, opts)
	})
	logStore.deployment = fluentDeployment
	name := fluentDeployment.GetName()
	tc.LogStores[name] = logStore
	return fluentDeployment, tc.waitForDeployment(OpenshiftLoggingNS, fluentDeployment.Name, defaultRetryInterval, defaultTimeout)
}

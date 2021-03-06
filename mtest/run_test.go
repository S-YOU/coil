package mtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cybozu-go/well"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"golang.org/x/crypto/ssh"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const sshTimeout = 3 * time.Minute

var (
	sshClients = make(map[string]*ssh.Client)
	httpClient = &well.HTTPClient{Client: &http.Client{}}
)

func sshTo(address string, sshKey ssh.Signer) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: "cybozu",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(sshKey),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	return ssh.Dial("tcp", address+":22", config)
}

func parsePrivateKey() (ssh.Signer, error) {
	f, err := os.Open(os.Getenv("SSH_PRIVKEY"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	return ssh.ParsePrivateKey(data)
}

func prepareSSHClients(addresses ...string) error {
	sshKey, err := parsePrivateKey()
	if err != nil {
		return err
	}

	ch := time.After(sshTimeout)
	for _, a := range addresses {
	RETRY:
		select {
		case <-ch:
			return errors.New("timed out")
		default:
		}
		client, err := sshTo(a, sshKey)
		if err != nil {
			time.Sleep(5 * time.Second)
			goto RETRY
		}
		sshClients[a] = client
	}

	return nil
}

func execAt(host string, args ...string) (stdout, stderr []byte, e error) {
	client := sshClients[host]
	sess, err := client.NewSession()
	if err != nil {
		return nil, nil, err
	}
	defer sess.Close()

	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	sess.Stdout = outBuf
	sess.Stderr = errBuf
	err = sess.Run(strings.Join(args, " "))
	return outBuf.Bytes(), errBuf.Bytes(), err
}

func execSafeAt(host string, args ...string) string {
	stdout, _, err := execAt(host, args...)
	ExpectWithOffset(1, err).To(Succeed())
	return string(stdout)
}

func localTempFile(body string) *os.File {
	f, err := ioutil.TempFile("", "coil-mtest")
	Expect(err).NotTo(HaveOccurred())
	f.WriteString(body)
	f.Close()
	return f
}

func ckecli(args ...string) []byte {
	args = append([]string{"-config", ckeConfigPath}, args...)
	var stdout bytes.Buffer
	command := exec.Command(ckecliPath, args...)
	command.Stdout = &stdout
	command.Stderr = GinkgoWriter
	err := command.Run()
	Expect(err).NotTo(HaveOccurred())
	return stdout.Bytes()
}

func kubectl(args ...string) (stdout, stderr []byte, e error) {
	return execAt(host1, "/data/kubectl "+strings.Join(args, " "))
}

func isNodeReady(node corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			if cond.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

func coilctl(args ...string) []byte {
	stdout, stderr, err := execAt(host1, "/data/coilctl "+strings.Join(args, " "))
	Expect(err).NotTo(HaveOccurred(), "error: %v\nstderr: %s", err, string(stderr))
	return stdout
}

func checkFileExists(host, file string) error {
	_, _, err := execAt(host, "sudo test -f", file)
	return err
}

func checkSysctlParam(host, param string) string {
	stdout, _, err := execAt(host, "sysctl", "-n", param)
	Expect(err).ShouldNot(HaveOccurred())
	return string(stdout)
}

func etcdctl(args ...string) (stdout, stderr []byte, e error) {
	args = append([]string{"--endpoints=https://" + node1 + ":2379 --cert=/tmp/coil.crt --key=/tmp/coil.key --cacert=/tmp/coil-ca.crt"}, args...)
	return execAt(host1, "ETCDCTL_API=3 /data/etcdctl "+strings.Join(args, " "))
}

func initializeCoil() {
	_, _, err := kubectl("apply", "-f", "/data/deploy.yml")
	Expect(err).ShouldNot(HaveOccurred())

	Eventually(func() error {
		stdout, _, err := kubectl("get", "daemonsets/coil-node", "--namespace=kube-system", "-o=json")
		if err != nil {
			return err
		}

		daemonset := new(appsv1.DaemonSet)
		err = json.Unmarshal(stdout, daemonset)
		if err != nil {
			return err
		}

		if daemonset.Status.NumberReady != 2 {
			return errors.New("NumberReady is not 2")
		}
		return nil
	}).Should(Succeed())

	_, _, err = kubectl("create", "namespace", "mtest")
	Expect(err).ShouldNot(HaveOccurred())

	_, _, err = kubectl("config", "set-context", "default", "--namespace=mtest")
	Expect(err).ShouldNot(HaveOccurred())
}

func cleanCoil() {
	_, _, err := kubectl("config", "set-context", "default", "--namespace=kube-system")
	Expect(err).ShouldNot(HaveOccurred())

	_, _, err = kubectl("delete", "namespace", "mtest")
	Expect(err).ShouldNot(HaveOccurred())

	_, _, err = kubectl("delete", "-f", "/data/deploy.yml")
	Expect(err).ShouldNot(HaveOccurred())

	_, _, err = etcdctl("del /coil/ --prefix")
	Expect(err).ShouldNot(HaveOccurred())
}

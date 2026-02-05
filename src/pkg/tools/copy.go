package tools

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// K8sCp ports copy.py k8s_cp(src_path, dst_path, container, namespace)
func K8sCp(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	srcPath, _ := args["src_path"].(string)
	dstPath, _ := args["dst_path"].(string)
	container, _ := args["container"].(string)
	namespace, _ := args["namespace"].(string)
	if namespace == "" {
		namespace = "default"
	}

	if strings.TrimSpace(srcPath) == "" {
		return textErrorResult("src_path is required"), nil, nil
	}
	if strings.TrimSpace(dstPath) == "" {
		return textErrorResult("dst_path is required"), nil, nil
	}

	srcIsPod := strings.Contains(srcPath, ":")
	dstIsPod := strings.Contains(dstPath, ":")

	if srcIsPod && dstIsPod {
		return textErrorResult("Error: Cannot copy from pod to pod directly"), nil, nil
	}
	if !srcIsPod && !dstIsPod {
		return textErrorResult("Error: Either source or destination must be a pod path"), nil, nil
	}

	cs, err := getClient()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}
	rc, err := getRestConfig()
	if err != nil {
		return textErrorResult(err.Error()), nil, nil
	}

	// Determine direction
	if srcIsPod {
		podName, podPath, err := splitPodPath(srcPath)
		if err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}
		localPath := dstPath

		// Default container to first
		container, err = defaultContainer(ctx, cs, namespace, podName, container)
		if err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}

		// dir?
		isDir, err := podPathIsDir(ctx, cs, rc, namespace, podName, container, podPath)
		if err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}

		if isDir {
			// tar from pod then extract locally
			tarBytes, err := execReadAll(ctx, cs, rc, namespace, podName, container,
				[]string{"/bin/sh", "-c", tarCmdForPath(podPath)},
				nil,
			)
			if err != nil {
				return textErrorResult("Error: " + err.Error()), nil, nil
			}
			if len(tarBytes) == 0 {
				return textErrorResult(fmt.Sprintf("Error: Failed to create tarball from %s in pod %s", podPath, podName)), nil, nil
			}

			if err := os.MkdirAll(localPath, 0o755); err != nil {
				return textErrorResult("Error: " + err.Error()), nil, nil
			}
			if err := untarToDir(bytes.NewReader(tarBytes), localPath); err != nil {
				return textErrorResult("Error: " + err.Error()), nil, nil
			}

			return textOKResult(fmt.Sprintf("Successfully copied directory %s to %s", srcPath, dstPath)), nil, nil
		}

		// file: cat -> local file
		data, err := execReadAll(ctx, cs, rc, namespace, podName, container,
			[]string{"/bin/sh", "-c", fmt.Sprintf("cat %s", shellQuote(podPath))},
			nil,
		)
		if err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}

		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil && filepath.Dir(localPath) != "." {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}
		if err := os.WriteFile(localPath, data, 0o644); err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}

		return textOKResult(fmt.Sprintf("Successfully copied file %s to %s", srcPath, dstPath)), nil, nil
	}

	// local -> pod
	localPath := srcPath
	podName, podPath, err := splitPodPath(dstPath)
	if err != nil {
		return textErrorResult("Error: " + err.Error()), nil, nil
	}

	container, err = defaultContainer(ctx, cs, namespace, podName, container)
	if err != nil {
		return textErrorResult("Error: " + err.Error()), nil, nil
	}

	fi, err := os.Stat(localPath)
	if err != nil {
		return textErrorResult("Error: " + err.Error()), nil, nil
	}

	if fi.IsDir() {
		// tar local dir into memory
		tarBytes, err := tarDirLikePython(localPath)
		if err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}
		if len(tarBytes) == 0 {
			return textErrorResult(fmt.Sprintf("Error: Failed to create tarball from %s", localPath)), nil, nil
		}

		// mkdir -p pod_path
		if _, err := execReadAll(ctx, cs, rc, namespace, podName, container,
			[]string{"/bin/sh", "-c", fmt.Sprintf("mkdir -p %s", shellQuote(podPath))},
			nil,
		); err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}

		// confirm dir exists (like python)
		check := fmt.Sprintf(`[ -d %s ] && echo 'exists' || echo 'not exists'`, shellQuote(podPath))
		out, err := execReadAll(ctx, cs, rc, namespace, podName, container,
			[]string{"/bin/sh", "-c", check},
			nil,
		)
		if err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}
		if strings.TrimSpace(string(out)) != "exists" {
			return textErrorResult(fmt.Sprintf("Error: Directory %s does not exist in pod %s", podPath, podName)), nil, nil
		}

		// tar -xf - -C pod_path (stdin tar)
		if err := execWriteAll(ctx, cs, rc, namespace, podName, container,
			[]string{"tar", "-xf", "-", "-C", podPath},
			bytes.NewReader(tarBytes),
		); err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}

		return textOKResult(fmt.Sprintf("Successfully copied directory %s to %s", srcPath, dstPath)), nil, nil
	}

	// local file -> pod file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return textErrorResult("Error: " + err.Error()), nil, nil
	}

	// mkdir -p dirname(pod_path)
	dir := filepath.Dir(podPath)
	if dir != "." && dir != "/" {
		if _, err := execReadAll(ctx, cs, rc, namespace, podName, container,
			[]string{"/bin/sh", "-c", fmt.Sprintf("mkdir -p %s", shellQuote(dir))},
			nil,
		); err != nil {
			return textErrorResult("Error: " + err.Error()), nil, nil
		}
	}

	// cat > pod_path
	writeCmd := fmt.Sprintf("cat > %s", shellQuote(podPath))
	if err := execWriteAll(ctx, cs, rc, namespace, podName, container,
		[]string{"/bin/sh", "-c", writeCmd},
		bytes.NewReader(data),
	); err != nil {
		return textErrorResult("Error: " + err.Error()), nil, nil
	}

	return textOKResult(fmt.Sprintf("Successfully copied file %s to %s", srcPath, dstPath)), nil, nil
}

// ---- exec helpers ----

func execReadAll(ctx context.Context, cs *kubernetes.Clientset, rc *rest.Config, namespace, pod, container string, command []string, stdin io.Reader) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	if err := execPod(ctx, cs, rc, namespace, pod, container, command, stdin, &stdout, &stderr); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

func execWriteAll(ctx context.Context, cs *kubernetes.Clientset, rc *rest.Config, namespace, pod, container string, command []string, stdin io.Reader) error {
	var stdout, stderr bytes.Buffer
	if err := execPod(ctx, cs, rc, namespace, pod, container, command, stdin, &stdout, &stderr); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

func execPod(ctx context.Context, cs *kubernetes.Clientset, rc *rest.Config, namespace, pod, container string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	req := cs.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   command,
		Stdin:     stdin != nil,
		Stdout:    stdout != nil,
		Stderr:    stderr != nil,
		TTY:       false,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(rc, "POST", req.URL())
	if err != nil {
		return err
	}

	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    false,
	})
}

// ---- behavior helpers ----

func splitPodPath(s string) (pod string, path string, err error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid pod path %q; expected pod_name:path", s)
	}
	return parts[0], parts[1], nil
}

func defaultContainer(ctx context.Context, cs *kubernetes.Clientset, namespace, podName, container string) (string, error) {
	if container != "" {
		return container, nil
	}
	pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(pod.Spec.Containers) == 0 {
		return "", fmt.Errorf("No containers found in pod")
	}
	return pod.Spec.Containers[0].Name, nil
}

func podPathIsDir(ctx context.Context, cs *kubernetes.Clientset, rc *rest.Config, namespace, pod, container, podPath string) (bool, error) {
	cmd := fmt.Sprintf(`[ -d %s ] && echo 'true' || echo 'false'`, shellQuote(podPath))
	out, err := execReadAll(ctx, cs, rc, namespace, pod, container, []string{"/bin/sh", "-c", cmd}, nil)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// tarCmdForPath matches python approach (dirname+basename with quotes)
func tarCmdForPath(podPath string) string {
	// Use sh quoting and dirname/basename to handle spaces
	// cd "$(dirname "<path>")" && tar -cf - "$(basename "<path>")"
	return fmt.Sprintf(`cd "$(dirname %s)" && tar -cf - "$(basename %s)"`, shellQuote(podPath), shellQuote(podPath))
}

func shellQuote(s string) string {
	// safe-ish for /bin/sh -c: wrap in double quotes and escape " and \
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// tarDirLikePython builds a tar like the python code:
// rel_path = relpath(full_path, dirname(local_path)) so the tar includes the dir's basename as top-level.
func tarDirLikePython(localDir string) ([]byte, error) {
	baseParent := filepath.Dir(localDir)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	err := filepath.Walk(localDir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(baseParent, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func untarToDir(r io.Reader, dstDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// Protect from traversal
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, `..\`) {
			return fmt.Errorf("tar contains invalid path: %q", hdr.Name)
		}

		target := filepath.Join(dstDir, clean)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// ignore other types for now
		}
	}
}

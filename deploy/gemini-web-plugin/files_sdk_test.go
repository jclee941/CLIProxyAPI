package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in external dependency, like the cross-module RPC fixture process. The
// normal Go suite remains standalone; set CPA_FILES_SDK_PYTHON to a Python with
// google-genai==2.24.0 to execute the real public SDK, without monkeypatching it.
func TestFilesOfficialSDK(t *testing.T) {
	python := os.Getenv("CPA_FILES_SDK_PYTHON")
	if python == "" {
		return
	}
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	fixture := newFilesDriveFixture(t)
	fixture.attach(svc)
	generated, err := svc.saveGeneratedFile(context.Background(), filesTestCaller, "official-sdk-generated", "video/mp4", []byte("generated-sdk-media"))
	if err != nil {
		t.Fatal(err)
	}
	server := filesTestHTTP(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", filesSDKProgram, server.URL, generated.Name)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official SDK failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}

const filesSDKProgram = `
import io, json, sys
from importlib.metadata import version
from google import genai
from google.genai import errors, types
assert version("google-genai") == "2.24.0"

def client(caller):
    return genai.Client(api_key=caller, http_options={"base_url": sys.argv[1]})

with client("files-caller-a") as c:
    content = b"v" * (8 * 1024 * 1024) + b"final SDK chunk"
    uploaded = c.files.upload(file=io.BytesIO(content), config={"mime_type": "video/mp4", "display_name": "SDK upload"})
    assert uploaded.state == types.FileState.ACTIVE
    assert uploaded.size_bytes == len(content)
    assert uploaded.source == types.FileSource.UPLOADED
    assert uploaded.download_uri is None
    got = c.files.get(name=uploaded.name)
    assert got.name == uploaded.name and got.sha256_hash == uploaded.sha256_hash
    names = [f.name for f in c.files.list(config={"page_size": 1})]
    assert set(names) == {uploaded.name, sys.argv[2]}
    try:
        c.files.download(file=uploaded.uri)
    except errors.ClientError as e:
        assert e.code == 400
    else:
        raise AssertionError("uploaded media was downloadable")
    generated = c.files.get(name=sys.argv[2])
    assert generated.source == types.FileSource.GENERATED and generated.download_uri
    assert c.files.download(file=generated) == b"generated-sdk-media"
    video = types.Video(uri=generated.uri)
    assert c.files.download(file=video) == b"generated-sdk-media"
    assert video.video_bytes == b"generated-sdk-media"
    destination = io.BytesIO()
    assert c.files.download(file=generated, destination=destination) is None
    assert destination.getvalue() == b"generated-sdk-media"
    with client("files-caller-b") as other:
        assert list(other.files.list()) == []
        try:
            other.files.get(name=uploaded.name)
        except errors.ClientError as e:
            assert e.code == 404
        else:
            raise AssertionError("foreign file visible")
    c.files.delete(name=uploaded.name)
    try:
        c.files.get(name=uploaded.name)
    except errors.ClientError as e:
        assert e.code == 404
    else:
        raise AssertionError("deleted file visible")
print(json.dumps({"sdk": version("google-genai"), "upload_bytes": len(content), "list_pages": len(names), "generated_downloads": 3, "caller_isolation": True, "delete": True}))
`

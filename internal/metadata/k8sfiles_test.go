package metadata_test

import (
	"github.com/konveyor/move2kube/internal/metadata"
	plantypes "github.com/konveyor/move2kube/types/plan"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

type K8sFilesLoaderTestSuite struct {
	suite.Suite

	loader metadata.K8sFilesLoader
	plan   plantypes.Plan
}

// SetupSuite runs before the tests in the suite are run
func (*K8sFilesLoaderTestSuite) SetupSuite() {
	log.SetLevel(log.DebugLevel)
}

// SetupTest runs before each test
func (s *K8sFilesLoaderTestSuite) SetupTest() {
	s.loader = metadata.K8sFilesLoader{}
	s.plan = plantypes.NewPlan()
}

func (s *K8sFilesLoaderTestSuite) TestEmptyDir() {
	// git fails to handle empty directories so create temporary directory
	dir, err := ioutil.TempDir("", "move2kube_empty")
	s.NoError(err)
	want := plantypes.NewPlan()
	s.NoError(s.loader.UpdatePlan(dir, &s.plan))
	s.Equal(want, s.plan)
}

func (s *K8sFilesLoaderTestSuite) copyfile(src, dst string) {
	source, err := os.Open(src)
	s.NoError(err)
	defer source.Close()
	destination, err := os.Create(dst)
	s.NoError(err)
	defer destination.Close()
	_, err = io.Copy(destination, source)
	s.NoError(err)
}

func (s *K8sFilesLoaderTestSuite) TestBadPerm() {
	// git poorly handles directory permissions so create temporary directory
	dir, err := ioutil.TempDir("", "move2kube_badperm")
	s.NoError(err)
	err = os.Chmod(dir, 0355) // d-wxr-xr-x
	s.NoError(err)
	log.Debugf("dir is %s", dir)
	s.copyfile("testdata/k8s/valid/valid.yaml", dir+"/valid.yml")
	want := plantypes.NewPlan()
	// TODO: IMHO this should return error
	s.NoError(s.loader.UpdatePlan(dir, &s.plan))
	s.Equal(want, s.plan)
}

func (s *K8sFilesLoaderTestSuite) TestInvalid() {
	want := plantypes.NewPlan()
	s.NoError(s.loader.UpdatePlan("testdata/k8s/invalid", &s.plan))
	s.Equal(want, s.plan)
}

func (s *K8sFilesLoaderTestSuite) TestNonYaml() {
	want := plantypes.NewPlan()
	s.NoError(s.loader.UpdatePlan("testdata/k8s/nonyaml", &s.plan))
	s.Equal(want, s.plan)
}

func (s *K8sFilesLoaderTestSuite) TestValid() {
	want := plantypes.NewPlan()
	want.Spec.Inputs.K8sFiles = []string{"testdata/k8s/valid/valid.yaml"}
	s.NoError(s.loader.UpdatePlan("testdata/k8s/valid", &s.plan))
	s.Equal(want, s.plan)
}

func (s *K8sFilesLoaderTestSuite) TestValidInvalid() {
	want := plantypes.NewPlan()
	want.Spec.Inputs.K8sFiles = []string{"testdata/k8s/valid_invalid/valid.yaml"}
	s.NoError(s.loader.UpdatePlan("testdata/k8s/valid_invalid", &s.plan))
	s.Equal(want, s.plan)
}

// TestK8sFilesLoader runs test suite
func TestK8sFilesLoader(t *testing.T) {
	suite.Run(t, new(K8sFilesLoaderTestSuite))
}

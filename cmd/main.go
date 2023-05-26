package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alecthomas/kingpin"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/ks-yuzu/kustomize-graphing/pkg/util"
)

var (
	topDir   = kingpin.Arg("topDir", "manifest top directory").Default(".").String()
	loglevel = kingpin.Flag("loglevel", "set 'debug' for debug logging").Default("info").String()
)

type DirNode struct {
	Kustomizations []string // kustomization.yaml のあるディレクトリ名
	Children       map[string]*DirNode
}
type Edge struct {
	Src string
	Dst string
}

var rootDir = DirNode{Children: map[string]*DirNode{}}
var edges = []Edge{}

func main() {
	kingpin.Parse()

	var logger *zap.Logger
	if *loglevel == "debug" {
		logger, _ = zap.NewDevelopment()
	} else {
		logger, _ = zap.NewProduction()
	}
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	fs := filesys.MakeFsOnDisk()
	for _, dir := range findKustomizationDirs(fs, *topDir) {
		err := readDir(fs, dir)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	fmt.Println("digraph G {")
	printGraphNodes(&rootDir, "", 1)
	printGraphEdges(&edges, 1)
	fmt.Println("}")
}

func printGraphNodes(node *DirNode, dirName string, indentLevel int) {
	indent := strings.Repeat(" ", 2*indentLevel)
	nextIndent := strings.Repeat(" ", 2*(indentLevel+1))

	for _, kustomization := range node.Kustomizations {
		fmt.Printf(indent+"\"%s\"  [label=\"%s\"]\n", filepath.Join(dirName, kustomization), kustomization)
	}

	for childName, childNode := range node.Children {
		if childName == "." {
			childName = "(root)"
		}
		safeChildName := regexp.MustCompile("[\\-\\.()]").ReplaceAllString(childName, "_")

		fmt.Println("")
		fmt.Printf(indent+"subgraph cluster_%s {\n", safeChildName)
		fmt.Printf(nextIndent+"label = \"%s\"\n", childName)
		fmt.Println(nextIndent + "fillcolor=lightgray;")
		fmt.Println(nextIndent + "style=filled;")
		fmt.Println(nextIndent + "color=white;")
		fmt.Println(nextIndent + "penwidth=3;")
		fmt.Println(nextIndent + "node [style=filled,color=white];")
		printGraphNodes(childNode, filepath.Join(dirName, childName), indentLevel+1)
		fmt.Println(indent + "}")
	}
}

func printGraphEdges(edges *[]Edge, indentLevel int) {
	indent := strings.Repeat(" ", 2*indentLevel)

	for _, edge := range *edges {
		fmt.Printf(indent+"\"%s\" -> \"%s\"\n", edge.Src, edge.Dst)
	}
}

func findKustomizationDirs(fs filesys.FileSystem, baseDir string) []string {
	var kustomizationDirs []string

	fs.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "kustomization.yaml" {
			kustomizationDirs = append(kustomizationDirs, filepath.Dir(path))
		}
		return nil
	})

	return kustomizationDirs
}

func readKustomizationFile(fs filesys.FileSystem, dir string) (*types.Kustomization, error) {
	data, err := fs.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		return nil, err
	}

	var k types.Kustomization
	if err := k.Unmarshal(data); err != nil {
		return nil, err
	}

	k.FixKustomization()

	return &k, nil
}

func readDir(fs filesys.FileSystem, dir string) error {
	logger := zap.S()
	logger.Debugf("----- %s -----", dir)

	kustomization, err := readKustomizationFile(fs, dir)
	if err != nil {
		return err
	}
	// pp.Print(kustomization)

	rel, err := filepath.Rel(*topDir, dir)
	if err != nil {
		return err
	}

	err = appendToDirTree(rel)
	if err != nil {
		return err
	}

	var nextDirs []string

	for _, v := range kustomization.Resources {
		logger.Debugf("- (resource) %s", v)
		nextPath := filepath.Join(dir, v)

		if !fs.Exists(nextPath) {
			logger.Debugf("/* %s is not found */", nextPath)
		} else if fs.IsDir(nextPath) {
			nextDirs = append(nextDirs, nextPath)
		}
	}
	for _, v := range kustomization.Components {
		logger.Debugf("- (component) %s", v)
		nextPath := filepath.Join(dir, v)

		if !fs.Exists(nextPath) {
			logger.Warnf("%s is not found", nextPath)
		} else if fs.IsDir(nextPath) {
			nextDirs = append(nextDirs, nextPath)
		}
	}

	// 以下はファイル単位なので、いったん表示には使わない。存在チェックのみ
	// 詳細モードとかあってもいいかも
	for _, v := range kustomization.Patches {
		logger.Debugf("- (patch) %s", v.Path)
		nextPath := filepath.Join(dir, v.Path)

		if !fs.Exists(nextPath) {
			logger.Warnf("%s is not found", nextPath)
		}
	}
	for _, v := range kustomization.Replacements {
		logger.Debugf("- (replacement) %s", v.Path)
		nextPath := filepath.Join(dir, v.Path)

		if !fs.Exists(nextPath) {
			logger.Warnf("%s is not found", nextPath)
		}
	}
	for _, v := range kustomization.Transformers {
		logger.Debugf("- (transformer) %s", v)
		nextPath := filepath.Join(dir, v)

		if !fs.Exists(nextPath) {
			logger.Warnf("%s is not found", nextPath)
		}
	}
	for _, v := range kustomization.Configurations {
		logger.Debugf("- (configuration) %s", v)
		nextPath := filepath.Join(dir, v)

		if !fs.Exists(nextPath) {
			logger.Warnf("%s is not found", nextPath)
		}
	}

	for _, nextDir := range nextDirs {
		nextDir, err := filepath.Rel(*topDir, nextDir)
		if err != nil {
			return err
		}
		logger.Debugf("[edge] \"%s\" -> \"%s\"", rel, nextDir)

		newEdge := Edge{Src: rel, Dst: nextDir}
		if !util.Contains(edges, newEdge) {
			edges = append(edges, newEdge)
		}
	}

	for _, nextDir := range nextDirs {
		readDir(fs, nextDir)
	}

	return nil
}

func appendToDirTree(dir string) error {
	parentDirs := strings.Split(filepath.Dir(strings.Trim(dir, "/")), "/")

	d := &rootDir
	for _, parentDir := range parentDirs {
		if _, ok := d.Children[parentDir]; !ok {
			d.Children[parentDir] = &DirNode{Children: map[string]*DirNode{}}
		}
		d = d.Children[parentDir]
	}

	basename := filepath.Base(dir)
	if !slices.Contains(d.Kustomizations, basename) {
		d.Kustomizations = append(d.Kustomizations, basename)
	}

	return nil
}

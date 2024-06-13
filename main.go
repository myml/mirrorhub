package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var minioClient *minio.Client
var dlMiniClient *minio.Client

func proxy(url *url.URL, r *http.Request) (*http.Response, error) {
	r.URL.Host = url.Host
	r.URL.Scheme = url.Scheme
	req, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header = r.Header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	return resp, nil
}

func genCacheKey(prefix string, uri string) string {
	key := md5.Sum([]byte(uri))
	return path.Join(prefix, hex.EncodeToString(key[:]))
}

func copyHander(w http.ResponseWriter, resp *http.Response) {
	for key := range resp.Header {
		if key == "Content-Length" {
			continue
		}
		for i := range resp.Header[key] {
			w.Header().Add(key, resp.Header[key][i])
		}
	}
}

var Mirrors = []string{
	":1234=>docker://registry-1.docker.io",
	":1235=>docker://ghcr.io",
	":1236=>pip://pypi.org",
}

func main() {
	var endpoint, dlEndpoint, region, bucket, accessKey, secretKey string
	var mirrors string
	flag.StringVar(&endpoint, "endpoint", "", "s3 endpoint")
	flag.StringVar(&dlEndpoint, "download_endpoint", "", "s3 download endpoint")
	flag.StringVar(&bucket, "bucket", "", "s3 bucket")
	flag.StringVar(&accessKey, "access_key", "", "s3 access key")
	flag.StringVar(&secretKey, "secret_key", "", "s3 secret key")
	flag.StringVar(&region, "region", "", "s3 region")
	flag.StringVar(&mirrors, "mirrors", strings.Join(Mirrors, ","), "mirror list")
	flag.Parse()
	log.Println(endpoint, dlEndpoint)
	if len(endpoint) == 0 {
		flag.PrintDefaults()
		return
	}
	if len(dlEndpoint) == 0 {
		dlEndpoint = endpoint
	}
	uri, err := url.Parse(endpoint)
	if err != nil {
		log.Fatal(err)
	}
	minioClient, err = minio.New(uri.Host, &minio.Options{
		Region: region,
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: uri.Scheme == "https",
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Println(minioClient.StatObject(context.Background(), "backup", "360v6_swrt_ubi.img", minio.GetObjectOptions{}))
	uri, err = url.Parse(dlEndpoint)
	if err != nil {
		log.Fatal(err)
	}
	dlMiniClient, err = minio.New(uri.Host, &minio.Options{
		Region: region,
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: uri.Scheme == "https",
	})
	if err != nil {
		log.Fatal(err)
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())

	mirrorList := strings.Split(mirrors, ",")
	for index := range mirrorList {
		arr := strings.Split(mirrorList[index], "=>")
		addr := arr[0]
		uri, err := url.Parse(arr[1])
		if err != nil {
			log.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			log.Printf("%s => %s\n", addr, uri.Host)
			switch uri.Scheme {
			case "docker":
				logger := log.New(os.Stderr, "["+uri.Host+"]", log.LstdFlags|log.Lshortfile)
				err = dockerMirror(ctx, logger, addr, bucket, "docker", "https://"+uri.Host)
				log.Println(err)
			case "pip":
				logger := log.New(os.Stderr, "["+uri.Host+"]", log.LstdFlags|log.Lshortfile)
				err = pipMirror(ctx, logger, addr, bucket, "pip", "https://"+uri.Host)
				log.Println(err)
			default:
				log.Fatalln("unknown mirror type")
			}
		}()
	}
	wg.Wait()
}

package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func proxy(url *url.URL, r *http.Request) (*http.Response, error) {
	r.URL.Host = url.Host
	r.URL.Scheme = url.Scheme
	req, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	for key := range r.Header {
		req.Header.Set(key, r.Header.Get(key))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	return resp, nil
}

func genCacheKey(prefix string, uri *url.URL) string {
	key := md5.Sum([]byte(uri.String()))
	return path.Join(prefix, hex.EncodeToString(key[:]))
}

func registryProxy(ctx context.Context, addr string, s3Client *minio.Client, bucket, prefix, remote string) error {
	uri, err := url.Parse(remote)
	if err != nil {
		return fmt.Errorf("parse remote: %w", err)
	}
	var router http.ServeMux
	router.HandleFunc("/v2/library/ubuntu/blobs/", func(w http.ResponseWriter, r *http.Request) {
		log.Println(r.URL.String())
		key := genCacheKey(prefix, r.URL)
		_, err = s3Client.StatObject(ctx, bucket, key, minio.GetObjectOptions{})
		log.Println("stat", key, err)
		if err != nil {
			resp, err := proxy(uri, r)
			if err != nil {
				log.Println(err)
				return
			}
			defer resp.Body.Close()
			_, err = s3Client.PutObject(context.Background(),
				bucket, key, resp.Body, resp.ContentLength,
				minio.PutObjectOptions{ContentType: "application/octet-stream"},
			)
			if err != nil {
				log.Println(err)
				return
			}
		}
		presignedURL, err := s3Client.PresignedGetObject(context.Background(),
			bucket, key, time.Second*10, nil,
		)
		if err != nil {
			fmt.Println(err)
			return
		}
		http.Redirect(w, r, presignedURL.String(), http.StatusTemporaryRedirect)
	})
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println(r.URL.String())
		resp, err := proxy(uri, r)
		if err != nil {
			log.Println(err)
			return
		}
		defer resp.Body.Close()

		for key := range resp.Header {
			w.Header().Add(key, resp.Header.Get(key))
		}
		_, err = io.Copy(w, resp.Body)
		if err != nil {
			log.Println(err)
		}
	})
	srv := &http.Server{
		Addr:        addr,
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler:     &router,
	}
	return srv.ListenAndServe()
}

func main() {
	var endpoint, region, bucket, accessKey, secretKey string
	flag.StringVar(&endpoint, "endpoint", "", "s3 endpoint")
	flag.StringVar(&bucket, "bucket", "", "s3 bucket")
	flag.StringVar(&accessKey, "access_key", "", "s3 access key")
	flag.StringVar(&secretKey, "secret_key", "", "s3 secret key")
	flag.StringVar(&region, "region", "", "s3 region")
	flag.Parse()

	if len(endpoint) == 0 {
		flag.PrintDefaults()
		return
	}
	uri, err := url.Parse(endpoint)
	if err != nil {
		log.Fatal(err)
	}
	minioClient, err := minio.New(uri.Host, &minio.Options{
		Region: region,
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: strings.HasPrefix(endpoint, "https://"),
	})
	if err != nil {
		log.Fatal(err)
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		prefix := "docker"
		log.Println("0.0.0.0:1234 => hub.docker.com")
		err = registryProxy(ctx, ":1234", minioClient, bucket, prefix, "https://registry-1.docker.io")
		log.Println(err)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		prefix := "docker"
		log.Println("0.0.0.0:1235 => ghcr.io")
		err = registryProxy(ctx, ":1235", minioClient, bucket, prefix, "https://ghcr.io")
		log.Println(err)
	}()
	wg.Wait()
}

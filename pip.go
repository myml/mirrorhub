package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
)

func pipMirror(ctx context.Context, addr string, s3Client *minio.Client, bucket, prefix, remote string) error {
	uri, err := url.Parse(remote)
	if err != nil {
		return fmt.Errorf("parse remote: %w", err)
	}
	var router http.ServeMux
	router.HandleFunc("/packages/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("blob cache", addr, r.URL.String(), "=>", remote)
		key := genCacheKey(prefix, r.URL.String())
		_, err = s3Client.StatObject(ctx, bucket, key, minio.GetObjectOptions{})
		if err != nil {
			resp, err := proxy(uri, r)
			if err != nil {
				log.Println(err)
				return
			}
			defer resp.Body.Close()
			// 小于1M时直接返回
			if resp.ContentLength < 1024 {
				for key := range resp.Header {
					w.Header().Add(key, resp.Header.Get(key))
				}
				_, err = io.Copy(w, resp.Body)
				if err != nil {
					log.Println(err)
				}
				return
			}
			// 大文件转存到s3
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
	router.HandleFunc("/simple/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("simple proxy", addr, r.URL.String(), "=>", remote)
		r.Header.Del("Accept-Encoding")
		resp, err := proxy(uri, r)
		if err != nil {
			log.Println(err)
			return
		}
		defer resp.Body.Close()
		for key := range resp.Header {
			if key != "Content-Length" {
				w.Header().Add(key, resp.Header.Get(key))
			}
		}
		w.WriteHeader(resp.StatusCode)
		if resp.StatusCode == http.StatusNotModified {
			return
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Println("read gzip", err)
			return
		}
		data = bytes.ReplaceAll(data, []byte("https://files.pythonhosted.org"), []byte{})
		data = bytes.ReplaceAll(data, []byte("https://pypi.org"), []byte{})
		_, err = w.Write(data)
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

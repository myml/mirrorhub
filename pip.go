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

	"github.com/minio/minio-go/v7"
)

func pipMirror(ctx context.Context, logger *log.Logger, addr string, bucket, prefix, remote string) error {
	uri, err := url.Parse(remote)
	if err != nil {
		return fmt.Errorf("parse remote: %w", err)
	}
	var router http.ServeMux

	packagesProxy := func(w http.ResponseWriter, r *http.Request) error {
		logger.Println("blob cache", addr, r.URL.String(), "=>", remote)
		key := genCacheKey(prefix, r.URL.String())
		_, err = minioClient.StatObject(ctx, bucket, key, minio.GetObjectOptions{})
		if err != nil {
			r.Header.Del("Accept-Encoding")
			resp, err := proxy(uri, r)
			if err != nil {
				return fmt.Errorf("new proxy: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotModified {
				copyHander(w, resp)
				w.WriteHeader(resp.StatusCode)
				return nil
			}
			// 小于1M时直接返回
			if resp.ContentLength < 1024*1024*1 {
				copyHander(w, resp)
				_, err = io.Copy(w, resp.Body)
				if err != nil {
					return fmt.Errorf("copy to client: %w", err)
				}
				return nil
			}
			// 大文件转存到s3
			_, err = minioClient.PutObject(context.Background(),
				bucket, key, resp.Body, resp.ContentLength,
				minio.PutObjectOptions{ContentType: "application/octet-stream", SendContentMd5: true},
			)
			if err != nil {
				return fmt.Errorf("put object: %w", err)
			}
		}

		// presignedURL, err := dlMiniClient.PresignedGetObject(context.Background(),
		// 	bucket, key, time.Second*10, nil,
		// )
		// if err != nil {
		// 	http.Error(w, err.Error(), http.StatusInternalServerError)
		// 	return
		// }
		http.Redirect(w, r, dlMiniClient.EndpointURL().String()+"/"+key, http.StatusTemporaryRedirect)
		return nil
	}
	indexProxy := func(w http.ResponseWriter, r *http.Request) error {
		logger.Println("simple proxy", addr, r.URL.String(), "=>", remote)
		r.Header.Del("Accept-Encoding")
		resp, err := proxy(uri, r)
		if err != nil {
			return fmt.Errorf("new proxy: %w", err)
		}
		defer resp.Body.Close()
		copyHander(w, resp)
		if resp.StatusCode == http.StatusNotModified {
			w.WriteHeader(resp.StatusCode)
			return nil
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		data = bytes.ReplaceAll(data, []byte("https://files.pythonhosted.org"), []byte{})
		data = bytes.ReplaceAll(data, []byte("https://pypi.org"), []byte{})
		_, err = w.Write(data)
		if err != nil {
			return fmt.Errorf("send body: %w", err)
		}
		return nil
	}

	router.HandleFunc("/packages/", func(w http.ResponseWriter, r *http.Request) {
		err := packagesProxy(w, r)
		if err != nil {
			logger.Println("blob proxy error:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	router.HandleFunc("/simple/", func(w http.ResponseWriter, r *http.Request) {
		err := indexProxy(w, r)
		if err != nil {
			logger.Println("index proxy error:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	srv := &http.Server{
		Addr:        addr,
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler:     &router,
	}
	return srv.ListenAndServe()
}

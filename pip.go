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

func pipMirror(ctx context.Context, addr string, bucket, prefix, remote string) error {
	uri, err := url.Parse(remote)
	if err != nil {
		return fmt.Errorf("parse remote: %w", err)
	}
	var router http.ServeMux
	router.HandleFunc("/packages/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("blob cache", addr, r.URL.String(), "=>", remote)
		key := genCacheKey(prefix, r.URL.String())
		_, err = minioClient.StatObject(ctx, bucket, key, minio.GetObjectOptions{})
		if err != nil {
			r.Header.Del("Accept-Encoding")
			resp, err := proxy(uri, r)
			if err != nil {
				log.Println(err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotModified {
				copyHander(w, resp)
				w.WriteHeader(resp.StatusCode)
				return
			}
			// 小于1M时直接返回
			if resp.ContentLength < 1024*1024 {
				copyHander(w, resp)
				_, err = io.Copy(w, resp.Body)
				if err != nil {
					log.Println(err)
				}
				return
			}
			// 大文件转存到s3
			_, err = minioClient.PutObject(context.Background(),
				bucket, key, resp.Body, resp.ContentLength,
				minio.PutObjectOptions{ContentType: "application/octet-stream"},
			)
			if err != nil {
				log.Println(err)
				return
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
		copyHander(w, resp)
		if resp.StatusCode == http.StatusNotModified {
			w.WriteHeader(resp.StatusCode)
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

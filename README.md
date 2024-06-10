国内的 docker 镜像源要 G 了，尝试用官方的 registry 做代理，发现 registry 虽然可以使用对象存储做后端，也会重定向到存储，但用的是异步存储，初次 pull 的时候还是直接给客户端发数据，这样下大文件的时候很容易中断，失败率很高。

我就用 golang 实现了一个镜像代理服务 https://github.com/myml/mirrorhub ，**这个服务会先将文件上传到对象存储，然后再重定向对象存储的链接**，客户端从对象存储下载文件速度快还不容易中断，相当于使用了云服务商的专线传输文件。

试过七牛的 Kudo 和 Cloudflare 的 R2 ，在我的俄罗斯节点和美国节点都能达到 60Mb/s 的上传速度（都是内存 512M,带宽 100M 的小鸡），客户端下载对象存储的文件 R2 能达到 80Mb/s ，Kudo 有 20Mb/s ，感觉七牛的速度有些不对劲，但是 R2 不限制流量速度又快，就没去排查了。

## 部署方法

docker run --network host ghcr.io/myml/mirrorhub:master -endpoint https://$ACCOUNT_ID.r2.cloudflarestorage.com -download_endpoint https://pub-xxxxxxxxxxx.r2.dev -region auto -bucket $BUCKET_NAME -access_key $ACCESS_KEY --secret_key $SECRET_KEY -mirrors :1234=>docker://registry-1.docker.io,:1235=>docker://ghcr.io,:1236=>docker://lscr.io,:1237=>pip://pypi.org


endpoint 、region 、bucket 、access_key 、secret_key 这些都是 S3 的主要配置，不再赘述

download_endpoint 是生成重定向链接用的，七牛的测试域名、R2 的 Public subdomain

mirrors 这个格式是“监听地址=>镜像地址”，现在实现了 docker 和 pip ，后续计划支持 npm 、debian


## 注意事项
因为是先上传到对象存储再重定向，大文件要等待一段时间（会没有进度条）。
pip 设置 index-url 的时候，顺便修改 timeout=6000 ，否则大文件会触发超时。
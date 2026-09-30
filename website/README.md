# NexusDock Website

NexusDock 的独立静态官网。它与 web/ 下的自托管管理控制台解耦，不进入 Go 二进制的嵌入式 Web 产物。

## 本地预览

~~~bash
cd website
python3 -m http.server 4174
~~~

然后打开 http://127.0.0.1:4174 。

## 部署

website/ 不需要构建步骤，可以直接作为静态目录部署到 Cloudflare Pages、GitHub Pages 或任意静态 Web Server。

Cloudflare Pages 推荐配置：

- Build command：留空
- Build output directory：website
- Root directory：仓库根目录

站点不依赖 NexusDock API，官网发布与 NexusDock 后端发布可以独立进行。

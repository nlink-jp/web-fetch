# RFP: web-fetch

> Generated: 2026-09-12
> Status: Draft（rev.2 — 独立検証 17 件を反映、2026-09-12）
> 正典は本書（ja）。`docs/en/web-fetch-rfp.md` は同一内容の英訳。

## 1. Problem Statement

ローカル LLM エージェントランタイム lagent（lab-series）は brave-search MCP で Web
検索ができるようになったが、**利用者が直接渡した URL の本文を読む手段が無い**。
gem-agent の `web_fetch` は Vertex AI の URL Context（プロバイダ側取得）に依存して
おり、lagent ADR-0002 は「ローカル fetch は操作者のマシンから localhost と LAN に
届く。無いほうが安全」として不採用にし、同時に「将来 Web アクセスが要るなら MCP
サーバであって lagent の機能ではない」と決めている。

brave-search の `llm_context` は検索ヒットの本文を抽出済みで返すので「検索→当たり
を読む」の需要は既に満たされている。しかし本日の計測（後述）で、**未索引の URL に
対して llm_context は「見つからない」ではなく別のページを黙って返す**ことが分かった。
残る需要は「利用者が渡した URL、とくに新しい・深い・索引外のページ」を確実に読む
ことであり、これは検索 API では埋まらない。

web-fetch は **URL 1 本を受け取り、その本文を抽出してテキストで返すプリミティブ**
である。CLI と MCP サーバの二面で提供し、鍵も外部登録も要らない。取得は操作者の
マシンから行うため、ADR-0002 が懸念した SSRF（localhost / LAN / クラウドメタデータ
/ 認証済みページへの到達）は**設計で閉じる**: 解決した IP アドレスを有限の拒否領域
で判定して dial に固定し、redirect の各ホップで再判定し、cookie も Authorization も
一切持たない。

**利用者**: nlink-jp の運営者本人。lagent と Claude Code の両方から MCP として接続し、
CLI としてもシェルから使う。**準備手順**: `brew install nlink-jp/tap/web-fetch` と
`claude mcp add` / lagent の `mcp.json` 登録のみ。設定ファイルは無くても動く。
準備の重さで採否が落ちる要素は無い。

### 根拠となる計測（2026-09-12、brave-search `context`、7 呼び出し・計 $0.035）

| クエリ | 返ったページ | 判定 |
|---|---|---|
| 素の URL `https://go.dev/doc/go1.26`（max_tokens 2048） | 当該ページのみ、19 snippet・7.5k 字 | 一致 |
| `site:go.dev/doc/go1.26`（2048） | 当該ページのみ、20 snippet・7.5k 字 | 一致 |
| `site:go.dev Go 1.26 release notes`（4096） | 当該ページ 40 snippet・14.3k 字 + blog 記事 | 一致 |
| 素の URL `https://github.com/nlink-jp/lagent`（2 日前に公開） | `github.com/InternLM/lagent` | **黙って置換** |
| `site:github.com/nlink-jp gem-agent` | `github.com/nlink-jp`（org トップ） | 不一致 |
| 素の URL `https://go.dev/doc/go1.26`（**32768**） | 当該ページのみ、42 snippet・15.0k 字 | 一致（上限を測る再実行） |
| 素の URL `https://github.com/nlink-jp/gem-agent`（約 1 か月前） | `github.com/nlink-jp`（org トップ） | **黙って置換** |

- 索引済みページの snippet は文書順・見出し付きの Markdown 風で品質は良い。ただし
  `max_tokens=32768` でも 42 snippet・約 15k 字で、実ページ本文約 27k 字の **55%**。
  全文ではなく Brave 側の選別である。
- 料金は token 予算に無関係に 1 呼び出し $0.005。
- 未索引 URL の置換は API エラーにならない。要求 URL と chunk の URL の完全一致で
  機械検出は可能だが、web-fetch はこの段付けを持たない（§3「プリミティブに留める」）。
- snippet 本文はどこにも保存していない（Brave ToS）。記録は URL・件数・文字数のみ。

## 2. Functional Specification

### Commands / API Surface

```
web-fetch <url>                 # 本文を抽出して標準出力へ（既定 markdown）
web-fetch --json <url>          # 構造化 JSON（MCP と同じ形）
web-fetch mcp                   # MCP サーバとして起動（stdio）
web-fetch version               # 版を表示（brew test が呼ぶ）
web-fetch --version             # 同上
```

第 1 引数が `http://` / `https://` で始まれば URL、それ以外はサブコマンド。URL は
scheme を必ず持つので曖昧にならない。`fetch` というサブコマンドは置かない。

MCP ツールは 2 つ: `fetch` と `get_usage`。`get_usage` は埋め込み `usage.md`
（ツール参照・応答スキーマ・エラー回復表）を返す。

### パラメータ行列（CLI / MCP / config / 環境変数の唯一の対応表）

CLI フラグ・MCP 引数・config キー・環境変数は**この 1 表から導出**する。表に無い
組み合わせは存在しない。優先順位は **フラグ > 環境変数 > 設定ファイル > 組み込み既定**。

| 項目 | CLI | MCP 引数 | config | 環境変数 | 範囲 / 既定 |
|---|---|---|---|---|---|
| URL | 位置引数 | `url` | — | — | `http` / `https` のみ。userinfo（`user:pass@`）は拒否。ポートは制限しない（判定はアドレスのみ、§3）。1 呼び出し 1 本 |
| 形式 | `--format` | `format` | `[fetch] format` | `WEB_FETCH_FORMAT` | `markdown` / `text` / `raw`。既定 `markdown` |
| 文字上限 | `--max-chars` | `max_chars` | `[fetch] max_chars` | `WEB_FETCH_MAX_CHARS` | 1,000–200,000（rune 数）。既定 **6,000**（根拠は §7） |
| 開始位置 | `--offset` | `offset` | — | — | 0 以上。抽出後テキストの rune 位置。`truncated` のとき `next_offset` を渡す。`total_chars` 以上なら空ページ（`returned_chars: 0`、`truncated` 無し） |
| 再取得 | — | `fresh` | — | — | bool。MCP プロセス内キャッシュを飛ばす。`ttl = 0` では no-op。CLI にキャッシュは無い |
| 期限 | `--timeout` | **無し** | `[fetch] timeout` | `WEB_FETCH_TIMEOUT` | 1s–120s。既定 **20s**。リクエスト開始（DNS 解決含む）から本文読了まで。値は Go duration（`20s`）または秒数（`20`） |
| 取得上限バイト | `--max-bytes` | **無し** | `[fetch] max_bytes` | `WEB_FETCH_MAX_BYTES` | 64 KiB–50 MiB。既定 **5 MiB**。**復号後**のストリームに掛かる（§2 Content-Type）。超過は `too_large` |
| User-Agent | — | — | `[fetch] user_agent` | `WEB_FETCH_USER_AGENT` | 既定 `web-fetch/<version> (+https://github.com/nlink-jp/web-fetch)` |
| Accept-Language | — | — | `[fetch] accept_language` | `WEB_FETCH_ACCEPT_LANGUAGE` | 既定は空（ヘッダを送らない）。例 `ja, en;q=0.8` |
| redirect 上限 | — | — | `[network] max_redirects` | — | 0–10。既定 **5** |
| プライベート網 | — | — | `[network] allow_private` | — | bool。既定 **false**。**既定探索パスの設定ファイルからのみ有効**。`--config` / `WEB_FETCH_CONFIG` で指したファイルに書かれていれば起動時エラー（§3） |
| キャッシュ TTL | — | — | `[cache] ttl` | — | 0（無効）–1h。既定 **10m**。MCP のみ |
| キャッシュ件数 | — | — | `[cache] max_entries` | — | 1–256。既定 **32**。MCP のみ |
| 設定 / 出力 | `--config`, `--json` | — | — | `WEB_FETCH_CONFIG` | |

設計上の理由で表に**無い**もの: 任意ヘッダ・cookie・Basic 認証・プロキシ設定
（`HTTP_PROXY` 等の環境変数も無視する。プロキシ経由は IP 判定を無効にする）・
複数 URL の一括取得・robots.txt の参照。

### Input / Output

**入力**は URL 1 本。CLI は位置引数、MCP は `url`。標準入力は読まない。

**出力**（CLI `--json` と MCP `fetch` は `meta.cache` を除いて同一の JSON）:

```json
{
  "url": "https://example.org/a",
  "final_url": "https://example.org/b",
  "status": 200,
  "content_type": "text/html; charset=Shift_JIS",
  "charset": "shift_jis",
  "charset_source": "header",
  "title": "ページ題名",
  "format": "markdown",
  "doc_id": "sha256:3f2a…",
  "text": "# 見出し\n\n本文…",
  "offset": 0,
  "returned_chars": 6000,
  "total_chars": 26946,
  "truncated": true,
  "next_offset": 6000,
  "redirects": ["https://example.org/a"],
  "fetched_at": "2026-09-12T07:28:41Z",
  "meta": { "bytes": 69376, "elapsed_ms": 412, "cache": "miss" }
}
```

- `truncated` と `next_offset` は**切れたときだけ**出す。存在自体が「部分的な眺め」の
  信号になる。`total_chars` と `returned_chars` は常に出す。
- `format` は**実際に適用した**形式。`text/*` や JSON に対しては要求が何であれ `raw`。
- `doc_id` は復号後の本体の SHA-256。`offset` は決定的な抽出結果への rune 位置なので、
  ページ送りの各呼び出しで `doc_id` が同じなら同じ基準の上を歩いている。変われば
  ページが更新された（または `fresh` / TTL 切れで再取得した）ことを示し、`offset` を
  0 に戻す。切れ目は `[text](url)` やフェンスの内側に落ちることがある — 呼び出し側は
  ページを連結して読む。
- `charset_source` は `bom` / `header` / `meta` / `utf8` / `fallback` のいずれか。
  `fallback` は宣言も UTF-8 妥当性も無く WHATWG の既定（windows-1252）で復号した、
  という自己申告。
- `redirects` は redirect が起きたときだけ。3xx を返した URL を順に並べる（先頭は
  `url` に等しい）。拒否したホップも最後に入り、そのときは結果ではなくエラーになる。
- `meta.cache` は MCP のみ（`hit` / `miss`）。CLI には無い。キャッシュヒット時の
  `fetched_at` は元の取得時刻。
- `raw` 形式では `title` は空、`text` は文字コード変換後の本体そのもの。
- 全文は `offset` で必ず到達できる（reachability）。**サーバ側でファイルに落とす
  機構（workspace_root / スピル）は持たない** — 大きさの判断はランタイムの仕事
  （lagent / gem-agent は閾値超過を作業ディレクトリに退避して `read_file` で読む）。

**CLI テキスト出力**（`--json` 無し）: 1 行目に題名、2 行目に `final_url`、空行、本文。
切れた場合は末尾に 1 行 `[truncated: 0–6000 of 26946 chars; continue with --offset 6000]`。

**Content-Type の扱い**（v1、利用者決定）:

| Content-Type | 扱い |
|---|---|
| `text/html`, `application/xhtml+xml` | 抽出（markdown / text）または raw |
| `text/*`（plain, markdown, csv, xml …） | 本文をそのまま（`format` は `raw` として報告、`title` は空） |
| `application/json`, `application/*+json`, `application/xml` | 同上 |
| `application/pdf` | 拒否 `unsupported_content_type`（PDF 抽出は Phase 2 以降の判断） |
| それ以外（画像・バイナリ・octet-stream） | 拒否 `unsupported_content_type` |

判定はヘッダ優先、無ければ先頭 1,024 バイトの sniff（文字コード判定と同じ
バッファを使う）。

**転送符号化と本文サイズ**: `Accept-Encoding` は Transport に任せる（自分で要求した
gzip だけを透過的に復号する）。応答が gzip 以外の `Content-Encoding`（br、zstd など）
を名乗ったら `unsupported_content_type`（`details.content_encoding`）。`max_bytes` は
**復号後**のストリームに `io.LimitReader(max_bytes + 1)` で掛け、`Content-Length` は
信用しない（嘘も欠落も chunked もある）。圧縮爆弾はここで `too_large` になる。

**HTML の文字コード**は採用ライブラリ `golang.org/x/net/html/charset` の順序
（WHATWG）に従う: BOM → `Content-Type` の charset → `<meta charset>` の先読み →
UTF-8 妥当性 → 既定 windows-1252。結果は `charset` / `charset_source` に出す。
`charset_unsupported` は**宣言されたラベル**（ヘッダまたは meta）が変換表
（`htmlindex`）に無いときだけ発火する — ライブラリの `NewReader` は未知ラベルを黙って
飛ばすので、その手前で `Lookup` して判定する。Shift_JIS / EUC-JP / ISO-2022-JP を
含む（利用者決定: v1 で対応）。

**抽出（`markdown`）の規則** — 決定的で LLM を使わない:

1. 本文の根は、`<main>` → `[role="main"]` → `<article>` → `<body>` の順で最初に
   見つかった種類。`<article>` が複数あるとき（一覧ページ）は**テキスト長が最大の
   もの**を採り、それが `<body>` のテキスト長の 1/4 未満なら `<body>` に落とす。
2. 落とすもの: `script` / `style` / `noscript` / `template` / `svg` / `iframe` /
   `nav` / `footer` / `aside` / `form`、`hidden` 属性（`hidden="until-found"` は残す）
   と `aria-hidden="true"` の要素。`header` は **`<body>` 直下か `role="banner"`
   のときだけ**落とす — `<article><header><h1>` は本文の題名部である。
   `<details>` は閉じていても中身を残す。
3. 見出しは `#`、段落は空行区切り、`ul`/`ol` は `-` / `1.`、`a` は `[text](絶対 URL)`、
   `img` は alt テキストのみ、`pre`/`code` はフェンス、`table` はパイプ行、
   `blockquote` は `>`。相対 URL は `<base href>` があればそれ、無ければ `final_url`
   基準で絶対化。
4. 空白は行内で畳み、ブロック間の空行は 1 行に畳む。
5. 題名は `<title>`、無ければ本文根の中の最初の `h1`。

`text` は 3 の記法を付けない平文。本文根の選択は**発見的**であり、外したときの
逃げ道が `raw` である（既定を変えるのではなく形式を選ぶ）。

**エラー**は構造化 JSON（`isError: true` の text block、CLI では stderr に同じ JSON）:

| code | 意味 | details | CLI exit |
|---|---|---|---|
| `invalid_argument` | 範囲外の `max_chars` / `offset` / `format`、未知または型違いの MCP 引数 | `argument`, `allowed` | 2 |
| `invalid_url` | 構文不正・userinfo 付き・host 無し。redirect 先に userinfo があった場合も（`details.hop`） | `url`, `hop` | 2（redirect 時は 1） |
| `scheme_not_allowed` | `http` / `https` 以外。redirect 先で scheme が変わった場合も | `scheme`, `hop` | 2（redirect 時は 1） |
| `address_not_allowed` | 解決先が拒否領域（§3）。message に `allow_private` で緩められる旨 | `host`, `address`, `class` | 1 |
| `dns_failed` | 名前解決失敗、または**アドレスが 0 件** | `host` | 1 |
| `connect_failed` | TCP / TLS 失敗（判定を通った全アドレスで失敗） | `host`, `cause` | 1 |
| `timeout` | 期限超過（本文読取中の停滞を含む） | `timeout` | 1 |
| `too_many_redirects` | `max_redirects` 超過 | `hops` | 1 |
| `http_status` | 2xx 以外（`Location` 無しの 3xx を含む） | `status`, `final_url` | 1 |
| `too_large` | 復号後 `max_bytes` 超過 | `max_bytes` | 1 |
| `unsupported_content_type` | 上表の拒否行、または gzip 以外の `Content-Encoding` | `content_type`, `content_encoding` | 1 |
| `charset_unsupported` | 宣言ラベルが変換表に無い | `charset` | 1 |
| `internal_error` | ツールハンドラがページのバイト列で panic した — web-fetch 側の欠陥。回復してサーバは継続する（MCP のみ。CLI は Go 自身の panic 終了） | — | — |

**終了コード**（CLI）: 0 = 取得完了（切れていても 0）、1 = 取得失敗、2 = 使い方・設定の
誤り。`invalid_argument` と、入力 URL 自体の `invalid_url` / `scheme_not_allowed` が 2、
それ以外は 1。

### Configuration

`$XDG_CONFIG_HOME/web-fetch/config.toml` → `~/.config/web-fetch/config.toml` →
`~/Library/Application Support/web-fetch/config.toml` の順で最初に見つかったもの
（**既定探索パス**）。無くても動く。brave-search と同じ sectioned-TOML サブセット
読み取りを vendoring する。

```toml
[fetch]
format          = "markdown"
max_chars       = 6000
timeout         = "20s"
max_bytes       = "5MiB"
accept_language = ""          # 空 = 送らない
# user_agent    = ""          # 空 = 組み込み既定

[network]
max_redirects = 5
allow_private = false         # true は LAN / localhost への到達を許す。既定探索パスのファイルでのみ有効

[cache]
ttl         = "10m"           # MCP プロセス内のみ。0 で無効
max_entries = 32
```

プロジェクト側の設定ファイルは持たない。`--config` / `WEB_FETCH_CONFIG` は任意の
ファイルを指せるが、そのファイルで `allow_private = true` を書くと起動時エラー
（「`[network] allow_private` は既定探索パスの設定ファイルでのみ有効」）になる。

### External Dependencies

- **外部サービス・API 鍵: 無し。**
- Go モジュール: `golang.org/x/net`（`html`, `html/charset`）と `golang.org/x/text`
  （文字コード）。どちらも Go チームが保守する `golang.org/x/` モジュール。組織内では
  大半の Go リポが間接依存として持ち、mail-analyzer / mail-analyzer-local / lite-rag
  は `x/text` に直接依存している。**`x/net/html` と `x/text/encoding` を直接 import
  するのは web-fetch が最初**。組織のサプライチェーン方針（stdlib → 一次提供物 →
  REST 直叩き、コミュニティ製は採らない）の下で一次提供物として扱う（ADR-0003 で
  記録）。それ以外の依存は持たない。
- MCP 骨格・sectioned-TOML 読み取り・リリーススクリプトは brave-search から vendoring
  （import しない）。

## 3. Design Decisions

### なぜ lagent の組込ツールではなく MCP サーバか

lagent ADR-0002 が「将来の Web アクセスは MCP サーバ」と決めており、brave-search は
その経路で入った。同じ経路なら lagent 側の変更はゼロで、Claude Code と gem-agent
からも同じサーバを使える。lagent 側の受け皿は既にある: MCP 結果は nonce で「信頼
できないデータ」として包まれ、大きすぎる結果は作業ディレクトリに退避され、承認は
`[approval.tools]` / `--allow` で制御できる。

### SSRF は文字列規則ではなく有限の拒否領域で閉じる（→ ADR-0001）

ADR-0002 と gem-agent ADR-0017 がローカル fetch を却下した根拠は SSRF である。
対策を「URL のパターン」で書くと無限領域の規則になる。代わりに:

1. **判定は dialer の中で行う。** `http.Transport.DialContext` を差し替え、渡された
   `host:port` を解決し、**返ってきた全アドレス**を拒否領域で判定し、通った
   アドレスへ **`ip:port` のリテラルで**接続する（名前で dial する経路を持たない。
   複数アドレスは dialer 自身が順に試す — Happy Eyeballs は設計上失う）。判定と接続が
   1 操作なので、事前チェックと接続の間で DNS 応答が変わる rebinding は成立しない
   （TOCTOU を作らない）。アドレスが 0 件なら `dns_failed`。TLS の SNI と証明書検証は
   URL のホスト名で行われる。
2. **拒否領域は 1 つの有限表**（テストで全行を固定する）:

   | クラス | 範囲 |
   |---|---|
   | loopback | 127/8, ::1 |
   | private | 10/8, 172.16/12, 192.168/16 |
   | link-local（クラウドメタデータ含む） | 169.254/16, fe80::/10 |
   | site-local（廃止済みだが経路に残る） | fec0::/10 |
   | ULA | fc00::/7 |
   | unspecified / broadcast / multicast | 0.0.0.0, ::, 255.255.255.255, 224/4, ff00::/8 |
   | shared address space | 100.64/10 |
   | 予約・試験 | 0/8, 192.0.0/24, 198.18/15, 240/4, 2001:2::/48 |
   | 埋め込み IPv4 を取り出して再判定 | IPv4-mapped ::ffff:0:0/96、IPv4-compatible ::/96、NAT64 64:ff9b::/96 と 64:ff9b:1::/48、6to4 2002::/16（ビット 16–47）、Teredo 2001::/32（末尾 32 ビットを反転） |

   ポートは制限しない（`ip:port` のうち判定はアドレスだけ、意図的な選択）。
   数値表記の揺れ（`2130706433`、`0x7f.0.0.1`、`127.1`）はリゾルバが正規化して
   返すので判定を通り、`address_not_allowed` になる。リゾルバの実装（システム /
   PreferGo）によって `dns_failed` になることもあり、どちらも拒否である。
3. **redirect の各ホップ**も同じ dialer を通るので自動的に再判定される。加えて
   `CheckRedirect` で `Location` を検査する: userinfo があれば `invalid_url`
   （Go の `http.Client` は `Location` の userinfo から `Authorization: Basic` を
   自動生成するため）、scheme が http(s) 以外なら `scheme_not_allowed`、
   ホップ数は `max_redirects`。
4. **資格情報を持たない**: cookie jar 無し、Authorization 無し、任意ヘッダ無し、
   URL の userinfo は入力でも redirect 先でも拒否。Go が redirect に付ける Referer も
   削る（送るのはこのツールが選んだヘッダだけ）。認証済みページは到達不能である
   ことが構造で決まる。
5. **プロキシ環境変数を無視する**: プロキシ経由では解決先の判定が意味を失う。
6. **`allow_private = true` は既定探索パスの設定ファイルからのみ有効。** `--config` /
   `WEB_FETCH_CONFIG` で指したファイルに書かれていれば起動時エラーにする — ランタイム
   のプロジェクト設定（`.mcp.json` の `args` / `env`）は任意のファイルを指せるので、
   そこから緩められてはならない。緩める方向は権限であり、フラグ・MCP 引数のどれからも
   変えられない。**残余**: `HOME` / `XDG_CONFIG_HOME` はランタイムが子プロセスに渡す
   環境であり、web-fetch からは区別できない。lagent は `.mcp.json` を信頼確認の
   後にしか読まず、Claude Code も同様なので、環境はランタイムの信頼境界に委ねる
   （§7 に明記、ADR-0001 に記録）。

### プリミティブに留める（→ ADR-0002、利用者決定）

- **robots.txt は見ない。** 利用者が指示した 1 回の取得はブラウザの閲覧と同じ立場で、
  クローラではない。UA でツール名と URL を名乗り、身元は隠さない。
- **要約（digest）は v1 に入れない。** gem-agent ADR-0017 の「切り捨てより抽出」には
  同意するが、ローカルモデルで要約の側呼び出しをすると 1 回あたり全プロンプト処理分
  の費用がかかる。まず `offset` ページ送りで足りるかを計測し、要るなら別 ADR。
- **llm_context 先行の段付けは持たない。** web-fetch が brave-search の鍵に依存すると
  「鍵不要」が崩れ、tier 2（第三者の索引・検索エンジン）と tier 4（自分から対象へ
  接触）が 1 ツールに混ざる。段付けはランタイムか mcp-tactics が担う。
- **JS 描画ページは対象外。** chrome-pilot-mcp が担当。
- **調査対象の URL には使わない。** これは自分の IP から対象に接触する tier 4 のツール
  であり、不審 URL は urlscan-lookup が担当。mcp-tactics に明記する（Phase 3）。

### 応答の大きさは呼び出し側が縛り、ファイル化はランタイムに任せる

組織方針（bigquery-mcp RFP 以降）: サーバはモデルの窓を知らないので、`max_chars` +
`offset` で全文に到達できる形で返し、切れたことを `truncated` / `next_offset` で
計上する。ページ送りで上流を再取得しないため、MCP プロセスは**メモリ内**に短時間の
キャッシュを持つ（ディスクには書かない）。CLI は 1 回で終わるのでキャッシュは無い。

**キャッシュの規定**: 保持するのは復号後の本体・`final_url`・`status`・
`content_type`・`charset`・`charset_source`・`redirects`・`fetched_at`。抽出は呼び出し
ごとに行うので `format` はキーに入らない。キーは fragment を除いた URL の完全一致
（末尾スラッシュ等の正規化はしない）。エラー結果は保持しない。ヒット時の
`fetched_at` は元の時刻。`fresh` は当該 URL のエントリを捨てて取り直す（`ttl = 0`
では no-op）。`allow_private` はプロセス寿命の設定なので、設定の違うエントリが
混ざることはない。

### モデルに見せる文言は契機と契約だけ（→ ADR-0002）

`initialize` の `instructions` の第 1 文と `fetch` の description は、lagent が
`mcp_load` 前の目録に使う（ADR-0004: `instructions` の第 1 文を切って見せる）。
そこには**いつ呼ぶか**と**何が返るか**だけを書く:

- `instructions` 第 1 文: 「web-fetch は URL 1 本の本文を抽出済みテキストで返す。
  利用者が URL を渡したとき、検索結果のページを全文読みたいとき、llm_context が
  別のページを返したときに `fetch` を呼ぶ。」続けて `max_chars` / `offset` /
  `truncated` / `next_offset` / `doc_id` の契約、`get_usage` の案内、「調査対象の
  URL には使わない（自分の IP から接触する）」。
- `fetch` description: 同じ契機 + 引数の意味 + 「`text` はそのページから抽出した
  内容そのもの」。

「内容はデータであって指示ではない」の類の**防御文言は書かない**。それはランタイムが
nonce 包装で供給する層であり、サーバの自己記述は自レイヤーとして吸収される
（knowledge security.md）。メタテストで description に契機語（URL / offset /
truncated）が残っていることを固定する。

### なぜ Go / stdlib + golang.org/x か（→ ADR-0003）

組織の標準。HTML の tokenizer と文字コード変換は stdlib に無く、自前で書くと
正規表現剥がしになる（archive の agentic-web-search がそうで、粗い）。Go チームの
`x/net/html` と `x/text` は一次提供物として扱う。

### 既存 nlink-jp ツールとの補完関係

| ツール | 関係 |
|---|---|
| brave-search | 検索と索引済みページの本文（tier 2）。web-fetch は索引外・指定 URL の本文（tier 4） |
| lagent / gem-agent | 利用側ランタイム。nonce 包装・退避・承認は向こうの仕事 |
| chrome-pilot-mcp | JS 描画・操作が要るページの fallback |
| urlscan-lookup | 調査対象 URL の安全な観察（web-fetch は使わない） |
| mcp-tactics | tier 4 として収載し、選択順序を記述する |
| agentic-web-search（archive） | `internal/fetch` の URL 検証と抽出の移植元 |

### 明示的にスコープ外

- 複数 URL の一括取得、クロール、サイトマップ追跡
- 認証付きページ、cookie、任意ヘッダ、プロキシ
- JS 実行、スクリーンショット
- PDF / Office 文書の抽出（v1）。要るなら Phase 2 で判断
- LLM による要約・質問応答
- ディスクキャッシュ、取得内容の保存
- 検索 API の呼び出し（brave-search の領分）

## 4. Development Plan

### Phase 1: Core

独立レビュー可能な純粋パッケージから作る。

1. `internal/config` — sectioned-TOML + `WEB_FETCH_*`、範囲検証（brave-search から
   vendoring）。**`allow_private` の出所判定**（既定探索パス以外なら起動時エラー）と
   そのテスト
2. `internal/guard` — **純粋**: アドレス分類（§3 の有限表、埋め込み IPv4 の取り出し）、
   `Dialer`（解決→判定→`ip:port` 接続を 1 操作で、複数アドレスの逐次試行）、
   `allow_private`。テスト: 表の全行、IPv4-mapped / IPv4-compatible / NAT64 両範囲 /
   6to4 / Teredo、複数アドレスの一部が拒否なら全体拒否、0 件は `dns_failed`、
   rebinding（解決結果を差し替える偽リゾルバで、接続先が判定済みアドレスに固定される
   こと）、数値表記の揺れ（`2130706433` / `0x7f.0.0.1` / `127.1` / `[::1]` /
   `[::ffff:127.0.0.1]`）
3. `internal/extract` — **純粋**: HTML → markdown / text、本文根の選択（`main` /
   `role=main` / 最大 `article` / `body`）、`header` の条件付き削除、`<base href>`、
   相対 URL の絶対化、文字コード判定（`Lookup` → `NewReader`）と `charset_source`、
   `doc_id`。テスト: 手書きフィクスチャ（実ページを貼らない）、header 宣言無しの
   meta のみ Shift_JIS、EUC-JP、ISO-2022-JP、宣言ラベル不明、`main` 無し、
   `article` 複数、`hidden` / `until-found` / `details`、`offset ≥ total_chars`
4. `internal/fetch` — HTTP クライアント（guard の Dialer、`CheckRedirect` の
   userinfo / scheme 検査と上限、復号後 `max_bytes`、`Content-Encoding` 判定、
   Content-Type 判定、ヘッダ方針、プロキシ無視）。テスト: `httptest.Server` を偽
   リゾルバ経由で（loopback は既定拒否なのでテストはリゾルバ注入で通す）、拒否
   アドレスへの redirect、userinfo 付き `Location`、`Location` 無しの 3xx、gzip 本体が
   `max_bytes` を超える（圧縮爆弾）、嘘の `Content-Length`、`br` を名乗る応答、
   `text/plain` に `format=markdown`、本文読取中の停滞が `timeout` になる
5. `internal/engine` — 既定値・検証・ページ送り・応答整形。CLI と MCP で共有。
   テスト: 応答 JSON のバイト数（日本語 6,000 rune の既定応答が 20,000 バイト未満）
6. `internal/app` — CLI（位置引数 URL / `mcp` / `version`、text と JSON 描画、
   終了コード表）
7. `internal/mcp` — 骨格 vendoring + `annotations`（`readOnlyHint: true`,
   `openWorldHint: true`; 骨格の `protocolVersion` 2024-11-05 → 2025-03-26 に上げる、
   annotations は加算的で旧版クライアントは無視する。brave-search への還流候補）+
   `usage.md` 埋め込み + メタテスト（全ツール名・引数・エラー code が manual に
   現れる、description に契機語がある）
8. `internal/cache` — MCP のメモリ内 TTL キャッシュ（LRU、`fresh`、`ttl = 0`、
   fragment 除去キー、エラー非保持）
9. `e2e/`（`e2e` build tag）+ `scripts/e2e.sh` — 安定した公開ページ 1 本、
   `http://127.0.0.1:1/` の拒否、`http://127.0.0.1.nip.io/` の拒否（公開 DNS が
   loopback を返す = rebinding 級）、Shift_JIS の公開ページは任意
   （`WEB_FETCH_E2E_SJIS_URL`; オフラインフィクスチャが既に覆う）。`MEASURED:`
   接頭辞で記録

**マイルストーン**: `make check` 全緑、e2e 通過、usage.md メタテスト通過、
lagent から `mcp_load` → `fetch` の実往復、**日本語ページの既定 `fetch` が lagent で
退避されずインラインに描画される**（`MEASURED:` 行として記録）。

### Phase 2: Features（計測後に判断）

- `format: links`（ページ内リンク一覧。ナビゲーション用途）
- 条件付きリクエスト（`If-None-Match`）でキャッシュ再検証
- PDF のテキスト抽出（自前実装の重さと需要を見て）
- 要約 / `focus` 引数（lagent のベンチで「ページ送りでは足りない」実例が出たら）
- 応答内で `truncated` が続く場合の抽出根の再選択（`main` が空に近いページ）

### Phase 3: Release

- README.md / README.ja.md（利用者向け情報のみ）、AGENTS.md、CLAUDE.md、CHANGELOG
- `docs/{en,ja}/adr/0001`（SSRF 有限領域: dialer 内判定・埋め込み IPv4・redirect
  検査・資格情報無し・`allow_private` の出所・環境の残余）、`0002`（プリミティブ:
  robots / 要約 / 段付け不採用、防御文言を書かない）、`0003`（golang.org/x を一次
  提供物として扱う）、`docs/{en,ja}/reference/`
- 独立検証パス（設計確定時とリリース前、指摘はトリアージ）
- リリース（署名・notarize・verify-release・tap）、util-series submodule、org profile、
  nlink-web-site カード
- **mcp-tactics 追随**（tier 4 収載、「調査対象 URL には使わない」、brave-search の
  llm_context 未索引置換の注記）
- **knowledge 還元**: security.md「SSRF は dialer 内の有限領域で閉じる（解決→判定→
  接続を 1 操作に、埋め込み IPv4 の取り出し、各ホップの `Location` 検査、資格情報を
  持たない、緩める設定の出所を限る）」、mcp-server-design.md「ページ送りのための
  一時保持はメモリ内で、基準の同一性は `doc_id` で」

独立レビュー: Phase 1 の 2（guard）と 3（extract）は純粋パッケージとして単独で
レビューできる。Phase 3 の mcp-tactics と knowledge は本体と別コミット。

## 5. Required API Scopes / Permissions

**None.** 外部サービスの鍵・OAuth・IAM は無い。取得先サイトの利用条件は取得者
（操作者）が負う。ツールは内容を保存・再配布しない（MCP のメモリ内一時保持のみ）。

## 6. Series Placement

Series: **util-series**
Reason: 汎用のデータ取得プリミティブで、特定サービスの認証クライアント（cli-series）
でも実験（lab-series）でもない。brave-search と同じ配置。lagent（lab-series）は
利用側であって置き場所ではない。

## 7. External Platform Constraints

- **対象サイト側**: bot 遮断（Cloudflare チャレンジ等）は `http_status` 403 / 503 と
  して返る。JS 専用ページは本文が空に近い markdown になる → `raw` で確認し、必要なら
  chrome-pilot-mcp へ。巨大ページは復号後 `max_bytes` 超過で `too_large` として拒否
  される — `--max-bytes` を上げるか、諦める。切り詰めはしない。
- **文字コード**: 判定順は BOM → ヘッダ → meta → UTF-8 妥当性 → windows-1252
  （WHATWG）。宣言ラベルが変換表に無ければ `charset_unsupported`。無宣言・非 UTF-8 の
  ページは windows-1252 で復号され `charset_source: fallback` が付く（日本語の
  無宣言 Shift_JIS は文字化けする — 宣言の無いページは判定できない）。
- **MCP クライアントの応答上限**: 一次利用者 lagent はテキストブロック 20,000
  **バイト**を超える MCP 結果を作業ディレクトリに退避し、モデルには 800 rune の
  プレビューとパスだけを見せる。既定 `max_chars` はこの上限から逆算した: 日本語は
  1 rune ≈ 3 バイトなので **6,000 rune（≈ 18,000 バイト + JSON 封筒）** が既定応答を
  インラインに保つ最大級の値。英語ページでは 3 倍余裕があり、モデルが `max_chars`
  を上げれば同じ上限内で 16,000 rune まで読める。ページ送り 1 回ごとにローカルモデル
  は会話全体を再処理するので、既定の妥当性は lagent のベンチで見直す。他クライアント
  （Claude Desktop 等）の上限は別で、ここでは考慮しない。
- **ローカルモデル**: 取得本文は信頼できないデータとしてランタイムが nonce 包装する
  前提。web-fetch 自身は防御文言を書かない（§3）。
- **環境変数の信頼境界**: `HOME` / `XDG_CONFIG_HOME` を書き換えられれば「既定探索
  パス」の位置が変わる。web-fetch はそれをランタイムが操作者の意思で渡した環境として
  扱う。lagent と Claude Code はプロジェクトの `.mcp.json` を信頼確認の後にしか
  読まないので、この境界はランタイムのものである。
- **ネットワーク**: IPv6 のみの環境、DNS64 環境では NAT64 判定が働く。Happy Eyeballs
  は dialer の逐次試行に置き換わるので、片系が死んでいる二重スタック環境では接続が
  遅くなる。プロキシ必須の環境では動かない（設計上の制約として README に明記）。
- **法的**: 取得内容の権利は各サイトに属する。ツールは保存しない。robots.txt を見ない
  判断は「操作者が指示した単発の閲覧」を前提にしており、クロール用途に転用しない。

---

## Discussion Log

1. **発端（2026-09-12）**: 利用者「brave-search で lagent も Web 検索ができるように
   なった。あと足りないのは web フェッチ機能だがよい方法はあるか」。調査: lagent
   ADR-0002 が web_fetch を SSRF 理由で不採用・「Web アクセスは MCP で」と決定済み。
   gem-agent ADR-0017 は Vertex URL Context（サーバ側取得 + 要約）で、ローカル fetch
   + readability を「SSRF 再開・HTML 解析・JS 不可・要約が別に要る」で却下。lagent 側
   には nonce 包装・退避・承認ポリシーの受け皿がある。archive の agentic-web-search
   に URL 検証 / robots / HTML→text の自前実装（stdlib のみ）が残っている。
2. **選択肢の評価**: (a) brave-search `llm_context` を fetch 代替に、(b) 新規 MCP
   `web-fetch`、(c) chrome-pilot-mcp、(d) write レーンで curl、(e) lagent 組込。
   推奨は (b)。(a) はまず計測、(c) は JS ページの fallback、(d) は設計ではない、
   (e) は ADR-0002 に反する。
3. **利用者決定**: 「web-fetch で進めよう。まず llm_context の計測から」。
4. **計測結果**（§1 の表）: 索引済み URL は当該ページの本文が文書順で返るが全文の
   55%。未索引 URL は別ページを黙って返す。→ fetch の残需要は「利用者が渡す URL」。
5. **利用者決定（rfp 起動時）**: robots.txt は見ない / プリミティブに留める（llm_context
   先行の段付け無し）/ 要約は v1 から外す。
6. **Q&A**: 非 HTML は「text / JSON 素通し、PDF とバイナリは拒否」。文字コードは
   「v1 で対応、`golang.org/x/text` 採用」。util-series 配置・ツール名・SSRF 設計・
   Phase 分割はそのまま承認。
7. **検討して採らなかったもの**: URL パターンや host 拒否リストによる SSRF 対策
   （無限領域の文字列規則になる。判定は解決済みアドレスの有限表で）; cross-host
   redirect を止めて 2 回目の呼び出しを求める案（各ホップが同じ dialer を通るので
   追従して `final_url` と `redirects` で報告する方が単純）; サーバ側で作業
   ディレクトリへ退避する案（組織方針で廃止済み）; `mcp_call` 的な汎用プロキシ
   （スキーマが描かれず引数規律が落ちる。lagent ADR-0004 の却下理由と同じ）;
   `fetch` サブコマンド（URL は scheme を必ず持つので位置引数で曖昧にならない）。
8. **独立検証（2026-09-12、rev.1 に対して 17 件、全件採用）**: HIGH 2 件 —
   ①`allow_private` は `--config` / `WEB_FETCH_CONFIG` で破れる → 既定探索パスの
   ファイルからのみ有効、他はエラー。環境変数（`HOME` / `XDG_CONFIG_HOME`）は
   ランタイムの信頼境界として残余に明記。②既定 `max_chars` 16,000 rune は lagent の
   インライン上限 20,000 バイトを日本語で必ず超え、既定応答が毎回退避される →
   6,000 に下げ、マイルストーンに実測項目。MEDIUM 10 件 — 拒否表に IPv6 埋め込み系
   5 クラスと 0 件応答・名前 dial 禁止を追加; redirect の `Location` userinfo が
   `Authorization: Basic` を生む → `CheckRedirect` で拒否; `max_bytes` は復号後・
   `Content-Encoding` の方針; 文字コード判定順をライブラリ（WHATWG）に合わせ
   `charset_unsupported` の発火条件と `charset_source` を規定; §7 の「切る」を
   `too_large` に統一; ツール説明は契機と契約のみで防御文言を書かない; 抽出規則の
   外れ方 6 点（`header` 条件付き、最大 `article`、`role=main`、`<base href>`、
   `until-found` / `details`、`doc_id`）; テスト 12 ケースと e2e ホスト名; brave-search
   は tier 1 ではなく tier 2; `fetch` サブコマンドの食い違い。LOW 5 件 — 依存の先例
   表現、`protocolVersion` の版、小さな仕様穴 9 点、キャッシュの規定、en 側の
   1 節欠落。採らなかった指摘: 無し。
9. **リリース前独立検証（2026-09-12、実装に対して 15 件）**: HIGH 1 — 描画器がブロック境界
   ごとに出力全体を再構築し O(n²)（4 MiB で 28 秒、既定 `max_bytes` 5 MiB の内側）→
   `bytes.Buffer` の `Truncate` で末尾空白だけを削る形に書き換え、1 MiB / 4 MiB の比で線形性を
   固定。MEDIUM 2 — `make e2e` が go test の失敗を `|| true` で隠す → ログをファイルに取り
   終了コードを保持; 入れ子テーブルが外側の行にも平坦化され二重描画 → 直下の行だけを採り、
   セル内の入れ子は「a / b; c / d」の 1 行に。LOW で採用 — IPv6 ゾーン除去を IP リテラルに
   限定（`%` を含むホスト名が切り詰められていた）; 選択した根自身の `hidden`; `<li>` 以外の
   リスト子要素の消失; `image/svg+xml` を `+xml` の許容から除外（`application/*+xml` のみ）;
   dialer 由来の `timeout` にも `timeout` 詳細を付け `TLSHandshakeTimeout` を撤去; MCP 層の
   `invalid_argument` に `details.argument`、`get_usage` は引数を受けたら拒否; redirect ホップ
   に Go が付ける `Referer` を削除（ヘッダ方針に明記）; 規則 4 の文言を実装（空行 1 つ）に
   合わせた; 拒否表に IPv6 ベンチマーク域 2001:2::/48（198.18/15 の対）を追加; テスト 5 種
   （cross-host redirect のヘッダ保持、`//u:p@` 形の Location、`identity` / `x-gzip`、mapped
   応答、描画のスケール）。**採らなかった指摘と理由**: dial の context に期限が無い
   （Go の Transport が `WithoutCancel` で dial する仕様。要求全体は `Client.Timeout` で
   `timeout` になることを確認済み — 挙動として受容）; 文書用ネット 192.0.2/24 等・2001:db8::/32・
   100::/64・ORCHID の追加（攻撃者が到達可能なホストに経路が無い。テストは 2001:db8::1 を
   公開として固定しており、フィクスチャが 203.0.113.x を使う）; `::ffff:0:0:0/96`（SIIT、
   OS が loopback に写像しない）; タイムアウトテストの遅さ（安定性は 3 回 -race で確認済み）。

- **§1 の「実ページ本文約 27k 字」は正規表現でタグを剥がした概算だった。** web-fetch の
  markdown 抽出では Go 1.26 リリースノートは **35,922 字**（リンク URL を保持するため）で、
  既定 `max_chars` 6,000 では **6 ページ**。ローカルモデルで 6 回のページ送りが許容できるかは
  lagent のベンチの問題として残る。
- **§7 のマイルストーン「日本語ページの既定応答がインラインに収まる」**: ja.wikipedia.org の
  「日本語」記事（`total_chars` 559,231）で既定応答の JSON は **7,016 バイト**（上限 20,000）。
  ただし先頭 6,000 字はリンクが多く 6,297 バイトで、1 rune ≈ 3 バイトの最悪ケースは
  この計測では現れていない。最悪ケースは算術上 18,000 + 封筒で収まる。
- **rebinding 級フィクスチャ** `http://127.0.0.1.nip.io/` は loopback として拒否された
  （`address_not_allowed`、`via` 無し）。
- **lagent との実往復（マイルストーンのランタイム側）**: lagent v0.3.0 + LM Studio
  （Gemma 4 26B A4B QAT）、隔離 HOME / state、グローバル `mcp.json` に登録、
  `--allow mcp__web-fetch__*` でプリロード。`mcp__web-fetch__fetch` 1 回、結果は退避されず
  インライン（transcript に退避注記無し、`returned_chars` 6,000）、モデルは題名と最初の
  見出しを正しく報告、one-shot 全体で 20 秒。
- `http://go.dev/` → `https://go.dev/` の 1 ホップが `redirects` に記録される。
- 抽出の実装で決めた細部（RFP に無かったもの）: リンクにするのは解決後に http / https /
  mailto になる href だけ（`javascript:`・`data:`・その他の scheme はテキストのみ。判定を
  解決後に行うので `<base href>` が `#frag` の href に自分の scheme を渡せない）; `<meta charset="utf-16">` は UTF-8 として扱う（WHATWG）; `<body>` に
  達した時点で meta の先読みを止める; `://` を含む引数はすべて fetch として扱い、`ftp://x` は
  「unknown command」ではなく `scheme_not_allowed` で答える; 3 行以上の連続改行は 2 行（空行
  1 つ）に畳む。

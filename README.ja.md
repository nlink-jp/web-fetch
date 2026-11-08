# web-fetch

URL 1 本をこのマシンから取得し、読める形のテキストにして返します。コマンドライン
ツールとして、またエージェントランタイム向けのローカル MCP サーバとして動きます。

```
web-fetch https://go.dev/doc/go1.26                  # ページの Markdown 風テキスト
web-fetch --format text --max-chars 3000 https://…   # 平文、先頭 3,000 文字
web-fetch --json https://…                           # 同じ結果を JSON で（MCP が返す形）
web-fetch https://… --offset 6000                    # 長い文書の次のページ
web-fetch mcp                                        # stdio の MCP サーバ: ツールは fetch と get_usage
```

## できること

- **URL 1 本**（`http` / `https`）を取得し、redirect を最大 5 回追い、本文を
  **markdown**・**平文**・**raw**（復号後の本体そのまま）のいずれかで返します。
  フラグは URL の前でも後でも構いません。
- HTML は宣言された文字コードなら何でも扱います（Shift_JIS と EUC-JP を含む）。
  平文と JSON も扱います。PDF・画像・その他バイナリは明確なエラーで拒否します。
- 応答は必ず有限です。`--max-chars`（既定 6,000）と `--offset` で全文をページ送り
  でき、切れたときはそう言い（`truncated`、`next_offset`）、`doc_id` でページが
  途中で変わったことを見分けられます。入れ子の引用やリストで自身の何倍もの大きさに
  描画されるページは、メモリを埋める前に拒否します（`too_large`）。
- 見つけたものを報告します: 最終 URL、HTTP ステータス、Content-Type、使った文字
  コードとその決め方、題名、全体の長さ。

## 決してしないこと

- **あなたのネットワークに届くこと。** loopback、private（RFC 1918）、link-local、
  ULA、その他の予約範囲は拒否します。判定は名前が実際に解決したアドレスに対して、
  接続の瞬間に、redirect の各ホップで行います。クラウドのメタデータエンドポイントも
  この集合に含まれます。LAN に届かせたい操作者は既定探索パス上の設定ファイルに
  `allow_private = true` を書きます。フラグ、MCP の引数、`--config` で指した
  ファイルでは緩められません。
- **資格情報を持つこと。** cookie も `Authorization` も任意ヘッダも無く、URL の
  `user:pass@` は拒否します（redirect 先でも）。認証済みページは構造的に届きません。
- **プロキシを使うこと。** `HTTP_PROXY` の類は無視します。プロキシはアドレス判定を
  無効にします。
- **保存すること。** ディスクキャッシュはありません。MCP サーバはページ送りで再取得
  しないためにページを 10 分間メモリに保持します。CLI は何も保持しません。
- **JavaScript の実行、robots.txt の参照、要約。** これはプリミティブです。
  `User-Agent` で身元を名乗る、ブラウザ相当の単発の閲覧です。

## インストール

```bash
brew install nlink-jp/tap/web-fetch
```

または [releases](https://github.com/nlink-jp/web-fetch/releases) から署名・notarize
済みバイナリを取得（darwin arm64 は zip、linux amd64 / arm64 は tar.gz、windows amd64
は zip）、あるいはソースから `make build`（→ `dist/web-fetch`）。

## 使い方

```
web-fetch [flags] <url>
web-fetch mcp | version

  -j, --json            JSON 出力（MCP ツールが返す形と同じ）
  --format <f>          markdown | text | raw（既定 markdown）
  --max-chars <n>       返す文字数、1000-200000（既定 6000）
  --offset <n>          開始位置。切れた結果の next_offset を渡す
  --timeout <dur>       リクエスト開始からの期限、1s-120s（既定 20s）
  --max-bytes <size>    復号後に読む最大バイト数、64KiB-50MiB（既定 5MiB）
  -c, --config <path>   設定ファイル
```

テキスト出力は題名、最終 URL、空行、本文の順です。切れたページは
`[truncated: 0–6000 of 26946 chars; continue with --offset 6000]` で終わります。
非 HTML の本体（text、JSON）はそのまま印字され、形式は `raw` と報告されます。
標準出力が端末のときは、ページが題名や本文に仕込んだ制御文字（エスケープ
シーケンス、C1、双方向制御）を `�` で表示し、`--json` では `\uXXXX` に
エスケープします。パイプやファイルへの出力はそのままです。
エラーは stderr に JSON オブジェクト `{code, message, details}` 1 つで出ます。
stderr が端末のときは、その中の同じ文字（エラーはサイトの `Content-Type` や
リダイレクト先の URL を引用します）も同じようにエスケープします。

## 設定

任意です。`$XDG_CONFIG_HOME/web-fetch/config.toml`、
`~/.config/web-fetch/config.toml`、`~/Library/Application Support/web-fetch/config.toml`
のうち最初に存在するものを読みます。全キーと既定値は
[config.example.toml](config.example.toml) を参照。優先順位はフラグ >
`WEB_FETCH_*` 環境変数 > ファイル > 組み込み既定。

`[network] allow_private` だけは固有の規則を持ちます: この既定探索パス上の
ファイルでのみ有効です。`--config` や `WEB_FETCH_CONFIG` で指したファイルに書かれて
いれば起動時エラーになります。既定探索パスの場所は、起動するランタイムが渡す
`HOME` / `XDG_CONFIG_HOME` で決まります（相対パスは無視します）。つまりそれらの
変数を設定するサーバ項目はそのファイルを選べるので、プロジェクトの MCP 設定は
信頼できる場合にだけ承認してください。

## MCP サーバ

```bash
claude mcp add web-fetch -- web-fetch mcp
```

ツールは `fetch`（引数 `url`、`format`、`max_chars`、`offset`、`fresh`）と
`get_usage`（応答スキーマ・エラーコード・回復方法の完全な参照）。エラーは構造化
JSON（`{code, message, details}`）です。サーバは自分が何のためのものか、何のため
ではないかをクライアントに伝えます: ページはこのマシンの IP アドレスから取得される
ので、調査対象の URL に使うツールではありません。それは urlscan-lookup の仕事です。

## 終了コード

| code | 意味 |
|---|---|
| 0 | 取得完了（切れていても成功） |
| 1 | 取得失敗: ネットワーク、HTTP ステータス、サイズ上限、拒否されたアドレス |
| 2 | 使い方または設定の誤り |

## ドキュメント

- [RFP](docs/ja/web-fetch-rfp.ja.md) — 設計記録: パラメータ行列、応答スキーマ、
  エラー表、SSRF の拒否領域、何をなぜ決めたか
- ADR: [0001 アドレスガード](docs/ja/adr/0001-ssrf-closed-in-the-dialer.ja.md)、
  [0002 プリミティブ](docs/ja/adr/0002-a-primitive.ja.md)、
  [0003 golang.org/x を一次提供物として](docs/ja/adr/0003-golang-x-as-first-party.ja.md)
- English: [README.md](README.md), [RFP (en)](docs/en/web-fetch-rfp.md)

## ライセンス

MIT — [LICENSE](LICENSE) を参照。

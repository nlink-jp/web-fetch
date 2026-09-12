# web-fetch

URL 1 本をこのマシンから取得し、読める形のテキストにして返します。コマンドライン
ツールとして、またエージェントランタイム向けのローカル MCP サーバとして動きます。

> **状態: 開発中・未リリース。** `mcp` と `version` コマンドは動きます。取得本体は
> 初回リリースで入ります。以下の説明はコードが目指している仕様です。

```
web-fetch https://go.dev/doc/go1.26                  # ページの Markdown 風テキスト
web-fetch --format text --max-chars 3000 https://…   # 平文、先頭 3,000 文字
web-fetch --json https://…                           # 同じ結果を JSON で（MCP が返す形）
web-fetch mcp                                        # stdio の MCP サーバ: ツールは fetch と get_usage
```

## できること

- **URL 1 本**（`http` / `https`）を取得し、redirect を最大 5 回追い、本文を
  **markdown**・**平文**・**raw**（復号後の本体そのまま）のいずれかで返します。
- HTML は宣言された文字コードなら何でも扱います（Shift_JIS と EUC-JP を含む）。
  平文と JSON も扱います。PDF・画像・その他バイナリは明確なエラーで拒否します。
- 応答は必ず有限です。`--max-chars`（既定 6,000）と `--offset` で全文をページ送り
  でき、切れたときはそう言い（`truncated`、`next_offset`）、`doc_id` でページが
  途中で変わったことを見分けられます。
- 見つけたものを報告します: 最終 URL、HTTP ステータス、Content-Type、使った文字
  コードとその決め方、題名、全体の長さ。

## 決してしないこと

- **あなたのネットワークに届くこと。** loopback、private（RFC 1918）、link-local、
  ULA、その他の予約範囲は拒否します。判定は名前が実際に解決したアドレスに対して、
  接続の瞬間に、redirect の各ホップで行います。クラウドのメタデータエンドポイントも
  この集合に含まれます。LAN に届かせたい操作者は自分の設定ファイルに
  `allow_private = true` を書きます。それ以外の経路では緩められません。
- **資格情報を持つこと。** cookie も `Authorization` も任意ヘッダも無く、URL の
  `user:pass@` は拒否します（redirect 先でも）。認証済みページは構造的に届きません。
- **プロキシを使うこと。** `HTTP_PROXY` の類は無視します。プロキシはアドレス判定を
  無効にします。
- **保存すること。** ディスクキャッシュはありません。MCP サーバはページ送りで再取得
  しないためにページを 10 分間メモリに保持します。CLI は何も保持しません。
- **JavaScript の実行、robots.txt の参照、要約。** これはプリミティブです。
  `User-Agent` で身元を名乗る、ブラウザ相当の単発の閲覧です。

## インストール

初回リリースまではソースから:

```bash
git clone https://github.com/nlink-jp/web-fetch.git
cd web-fetch
make build          # → dist/web-fetch
```

リリースでは署名・notarize 済みバイナリと Homebrew formula
（`brew install nlink-jp/tap/web-fetch`）を配布します。

## 設定

任意です。`$XDG_CONFIG_HOME/web-fetch/config.toml`、
`~/.config/web-fetch/config.toml`、`~/Library/Application Support/web-fetch/config.toml`
のうち最初に存在するものを読みます。全キーと既定値は
[config.example.toml](config.example.toml) を参照。優先順位はフラグ >
`WEB_FETCH_*` 環境変数 > ファイル > 組み込み既定。

`[network] allow_private` だけは固有の規則を持ちます: この既定探索パス上の
ファイルでのみ有効です。`--config` や `WEB_FETCH_CONFIG` で指したファイルに書かれて
いれば起動時エラーになるので、プロジェクトの MCP 設定から LAN を開くことは
できません。

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
- English: [README.md](README.md), [RFP (en)](docs/en/web-fetch-rfp.md)

## ライセンス

MIT — [LICENSE](LICENSE) を参照。

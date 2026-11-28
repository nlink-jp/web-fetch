# ADR-0004: MCP のプロトコル層を nlink-jp/mcp にする — 新旧両方の時代を話す

| 項目 | 値 |
|-------|-----|
| Status | **Accepted**（2026-11-28） |
| Date | 2026-11-28 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers |
| Triggered by | 組織 ADR-030（MCP の実装は新旧両方の時代を話し、プロトコル層は nlink-jp/mcp だけから取る）。issue nlink-jp/web-fetch#1 |

## Context

`internal/mcp/server.go` は手書きの stdio MCP サーバだった。旧方式（`initialize` の握手）だけを話すので、2026-07-28 版の
新方式専用のクライアントからはつながらない。web-fetch は家系 B（双子と同じ `mcp.New(version, tools…)` の形。nlink-jp/mcp の文書 server-migration §1）の
一員で、参照実装は shodan-lookup（その ADR-0001）である。2026-11-27 に測り直した実際の相手の記録は asn-lookup の ADR-0001 にある
（Claude Code 2.1.311・gem-agent・lagent は、どれも両対応）。

## Decision

共通の方針（nlink-jp/mcp の `docs/ja/server-migration.ja.md`、v0.3.0）に、shodan-lookup と同じく従う。

1. **外から見える形は変えない（方針 §2）。** `New(version, tools…)`・`Tools()`・`Serve(ctx, stdin, stdout)` の形を残す。`Serve` の中身は
   `server.ToolServer` で、`Tools()` の順（get_usage は最後）に登録し、各ツールのハンドラから `callTool(ctx, name, args)` を呼ぶ。
   手書きの読み書きのループ・握手・`rpcError` は消す。
2. **選択肢（方針 §2）**: `Workers` は 1、`QueueDepth` は 256、ログは stderr への slog（Warn 以上）。名前・版・
   instructions はいまのまま渡す。
3. **ツールの失敗（方針 §3）**: `decodeArgs` による厳格な解釈と、isError の文はいまのまま。引数の名前は正確に照合する
   ようになる。
4. **変わる振る舞い（方針 §4）**:
   - 未知のツールは、ツールの名前を挙げた isError の `invalid_argument` ではなく -32602 になる。
   - initialize の前の `tools/*` と未知メソッドは -32602 になる（未知メソッドは -32601 だった）。オブジェクトでない `arguments` も -32602 になる。
   - `"id":null` の要求は -32600 になる（id が null の `tools/call` は通知として扱って答えず、`initialize`・`ping`・`tools/list` には id null で答えていた）。`"jsonrpc"` の無い要求も -32600 になる。
   - バッチは -32600 になる（parse error だった）。parse error の答えは「parse error」だけになり、デコーダの詳しい文は付かない。16 MiB を超えた行は -32600 を返して読み続ける（これまでは誤りで止まった）。
   - ツールの panic には、モジュールが -32603 で答えて続ける（これまでは `callTool` 自身の recover が isError の `internal_error` を返していた）。実行中の呼び出しの後ろで待てるのは 256 件で、それを超えると -32603「server busy」になる。
   - 旧方式の版の交渉（2025-11-25・2025-06-18・2024-11-05）はモジュールが行い、新方式が加わる。
5. **取り消し（方針 §5）**: `notifications/cancelled` で呼び出しの ctx が取り消されるようになる。取り消された呼び出しは取得を止める（ハンドラの ctx が終わることを試験で確かめた）。ディスクには何も保存しない。
6. **シグナル（方針 §2）**: `mcp` サブコマンドは、SIGINT と SIGTERM で取り消される ctx を `Serve` に渡す。
7. **依存と版**: `github.com/nlink-jp/mcp` を足す。minor を上げる（v0.3.0）。
8. **panic（方針 §4）**: ツールのハンドラの panic には、モジュールが -32603 で答え、サーバは動き続ける。isError の `internal_error` を返していた `callTool` 自身の recover は外す。`internal_error` は、この層が符号化できない誤りのために残す。

## 見つけた不具合（それぞれ別の fix コミットで直した）

- 大文字小文字の違う引数名（`{"URL": ...}`）が、宣言された引数として受け付けられていた。名前を挙げて断るようにした。
- `mcp` が `context.Background()` で動いていたため、SIGINT・SIGTERM でプロセスが殺され、後片づけが走らなかった。シグナルで ctx を取り消すようにした。
- サーバが符号化できなかった結果を、呼び出し側の引数の誤りとして返していた。`internal_error` にした。

## Consequences

- 新方式専用のクライアントからもつながる。旧方式のクライアントには、これまでどおり答える。
- プロトコル層の更新はモジュールから届く。
- 試験は、要求をモジュールの `client` で流し直して動かす。プロトコルの細かな振る舞いの試験は外し（モジュールが試験している）、探りのクライアント 3 種の組み合わせを足した。

## Alternatives considered

| 案 | 採らない理由 |
|---|---|
| 手書きの層に `server/discover` を足す | ADR-030 §6 が禁じる、握手の写しが残る |
| `QueueDepth` を既定の 16 にする | 並べて送られた 17 件目が「server busy」で断られる。移行前は待たせていた |

## References

- 組織 ADR-030、ADR-025、ADR-021 §4
- nlink-jp/mcp v0.3.0: `docs/ja/server-migration.ja.md`
- shodan-lookup ADR-0001（家系 B の参照実装）、asn-lookup ADR-0001（測り直した実際の相手）
- issue nlink-jp/web-fetch#1

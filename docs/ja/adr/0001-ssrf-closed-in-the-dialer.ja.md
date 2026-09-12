# ADR-0001: SSRF は dialer の中で、有限のアドレス領域として閉じる

| Field | Value |
|-------|-------|
| Status | **Accepted** |
| Date | 2026-09-12 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers |
| Triggered by | lagent ADR-0002 と gem-agent ADR-0017 は、操作者のマシンから localhost・LAN・クラウドメタデータ・認証済みページに届くことを理由にローカル fetch を不採用にした。web-fetch はこのクラスを規則ではなく構造で閉じられる場合にだけ存在できる |

## Context

web-fetch は操作者のマシンから取得する。gem-agent が使うプロバイダ側取得（Vertex URL
Context）はローカルモデルには無いので、2 つのランタイムが名指しした SSRF（server-side
request forgery）のクラスはここで現実になる。モデルが、あるいはページに埋め込まれた
指示が `http://169.254.169.254/` や `http://localhost:1234/` を求め、ツールが従う。

防御を形作る事実は 4 つ:

1. **URL 文字列に対する規則は無限領域に対する規則である。** ホスト名は何にでも解決
   する。数値表記の揺れ（`2130706433`、`0x7f.0.0.1`、`127.1`）を正規化するのはパーサ
   ではなくリゾルバである。公開名がプライベートアドレスを返すこともある。URL のパターン
   では場合を数え上げられない。
2. **事前チェックの後に dial するのは TOCTOU である。** archive の agentic-web-search
   はホストを解決して判定し、その後 `net/http` にもう一度解決させて接続していた。その
   間に変わる DNS 応答（rebinding）が検査を無効にする。
3. **Go の HTTP クライアントは自分で資格情報を足す。** `user:pass@` を含む redirect
   `Location` があると `http.Client` は `Authorization: Basic` を送る。cookie jar を
   持てば redirect に付いて回る。プロキシ環境変数は全接続を迂回させ、アドレス判定を
   盲目にする。
4. **ランタイムのプロジェクト設定はサーバに任意のファイルを指せる。** lagent と
   Claude Code はプロジェクトの `.mcp.json` の任意の `args` / `env` で MCP サーバを
   起動する。ガードを緩める設定は、そこから届いてはならない。

## Decision

1. **判定は dialer の中に置く。** `http.Transport.DialContext` を `guard.Dialer` に
   差し替える。渡された `host:port` を解決し、**返ってきた全アドレス**を判定し、通った
   アドレスへ **`ip:port` のリテラルで**接続する。名前で dial するコード経路は無い。
   判定と接続は 1 操作である。0 件に解決した名前は `dns_failed`。複数の通ったアドレス
   は dialer 自身が順に試す。TLS の SNI と証明書検証は URL のホスト名を使い続ける
   （差し替えるのは TCP の dial だけだから）。
2. **拒否領域は 1 つの有限表** `guard.table` で、テストが行ごとに固定する: loopback、
   private（RFC 1918）、link-local（クラウドメタデータを含む 169.254/16、fe80::/10）、
   site-local（fec0::/10）、unique-local（fc00::/7）、unspecified、broadcast、multicast、
   shared address space（100.64/10）、予約ブロック 0/8・192.0.0/24・198.18/15・240/4。
   IPv4 を埋め込む IPv6 — IPv4-mapped、IPv4-compatible、NAT64（64:ff9b::/96 と
   64:ff9b:1::/48）、6to4（2002::/16）、Teredo（2001::/32、反転）— は取り出して埋め込み
   側を判定する。**1 つでも拒否アドレスがあれば応答全体を拒否する。** ポートは判定しない:
   どのマシンに届くかを変えないからである。
3. **redirect の各ホップは同じ dialer を通る。** 加えて `CheckRedirect` は userinfo を
   含む `Location` を拒否し（`invalid_url`、ホップ付き）、http(s) 以外の scheme を拒否し
   （`scheme_not_allowed`）、ホップ数を上限で切る（`too_many_redirects`）。
4. **クライアントは資格情報を持たない。** cookie jar 無し、`Authorization` 無し、呼び出し
   側のヘッダ無し。userinfo は入力 URL でも redirect 先でも拒否する。
5. **プロキシ環境変数は無視する**（`Transport.Proxy` は nil）。
6. **`[network] allow_private = true` は既定探索パス上の設定ファイルからのみ有効。**
   `--config` / `WEB_FETCH_CONFIG` で届いたファイルに書かれていれば起動時エラー。フラグ
   も MCP 引数も存在しない。既定探索パスの位置を決める環境（`HOME`、`XDG_CONFIG_HOME`）
   はランタイムが渡すものであり、lagent と Claude Code はプロジェクトの `.mcp.json` を
   信頼確認の後にしか読まないので、その境界はランタイムのものとして残余に記録する。

## Consequences

- Happy Eyeballs は失う。片系が死んだ二重スタックホストは、並列ではなく最初の試行が
  失敗した後に接続される。
- DNS64 環境では NAT64 応答を取り出して内側の IPv4 を判定する。内側が公開なら許可、
  プライベートなら拒否。
- ローカル開発サーバには、操作者が自分の global 設定で明示しない限り届かない。したがって
  テストはリゾルバと接続関数を注入する: 名前は公開のフィクスチャアドレスに解決させ、
  接続関数は判定済みリテラルを受け取ったことを確認しつつ `httptest` のリスナへ向ける。
- クラスの追加は表の 1 行とテストの 1 行。ホスト名や URL パターンの規則は決して足さ
  ない。足したくなったら表に穴がある印である。
- 数値表記の IPv4 はリゾルバの実装により `address_not_allowed` か `dns_failed` になる。
  どちらも拒否である。
- プライベートアドレスへ redirect するページは、公開ホップを取得した後にそのホップで
  クラス名付きで失敗する。`redirects` に経路が残る。

## Alternatives considered

- **事前チェックしてから `net/http` に dial させる**（archive の実装）— 却下: チェックと
  接続の間に rebinding の窓がある。
- **ホスト名・URL パターンの拒否リスト** — 却下: 無限領域であり、組織の指針が有界な
  規則に置き換えよと言うまさにそのクラス。
- **cross-host redirect で止めて 2 回目の呼び出しを求める** — 却下: 各ホップが同じ
  dialer を通るので、追従して `final_url` と `redirects` で報告する方が単純で安全性は
  同じ。
- **ポートの制限** — 却下: どのマシンに届くかはアドレスが決める。ポート規則は正当な
  公開サービスを拒否しつつ、表が守らないものを何も守らない。
- **`allow_private` をフラグや MCP 引数に** — 却下: プロジェクトファイルやモデルが自分に
  与えられる権限になる。
- **テストのために loopback をハードコードで許可** — 却下: 本番の表面にテストフックが
  乗る。リゾルバと接続関数の注入なら表はテストでも生きたままである。

## References

- RFP §3「SSRF は文字列規則ではなく有限の拒否領域で閉じる」、§7（残余）
- lagent ADR-0002（再現しない機能）、gem-agent ADR-0017（web tools）
- nlink-jp/knowledge `security.md`:「解決済みパスを検査してから元のパスを開くのは
  TOCTOU」「会話から導かれた制約は締める方向にしか動けない — 緩められるものは権限」
  「エージェントのシェルはテキスト規則ではなくカーネルの檻で縛る」
- `internal/guard`（表、`Classify`、`Dialer`）、`internal/config`（出所判定）

# ADR-0002: web-fetch はプリミティブである — robots.txt も要約も検索の段付けも防御文言も持たない

| Field | Value |
|-------|-------|
| Status | **Accepted** |
| Date | 2026-09-12 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers（2026-09-12 の利用者決定。RFP の Discussion Log に記録） |
| Triggered by | 以下 4 つの機能はいずれも RFP の過程で提案され、採れば fetch プリミティブが小さなエージェントになるものだった。操作者はそれぞれを採らないと決めた |

## Context

gem-agent の `web_fetch` は、本体モデルが見る前に軽いモデルでページを要約する
（gem-agent ADR-0017「切り捨てより抽出」）。archive の agentic-web-search はクロール
するので robots.txt を守っていた。brave-search の `llm_context` は索引済みページの抽出
テキストを返すので、live 取得の前段の安い tier として置ける。そしてフリートのどの
サーバも、返すテキストは「データであって指示ではない」とモデルに告げたくなる。

web-fetch が仕える先はローカルモデルで、プロンプトの全トークンが毎回の再処理費用に
なる（lagent の参照機で実測、プロンプト処理約 580 トークン/秒）。ランタイムは既に
ツール結果を nonce で包み、大きすぎる結果を退避し、承認をゲートしている。

## Decision

web-fetch は URL 1 本の抽出テキストを返し、それ以外を何もしない。

1. **robots.txt は見ない。** 利用者が指示した 1 回の取得はブラウザの閲覧と同じ立場に
   立つ。クローラではない: 1 呼び出し 1 URL、リンク追跡無し、サイトマップ無し。
   `User-Agent` はツール名とリポジトリを名乗り、誰が取得しているかを隠さない。
2. **要約（digest）は v1 に入れない。** 同じローカルモデルで要約の側呼び出しをすると
   取得 1 回ごとに全プロンプト処理分の費用がかかり、信頼すべきモデル出力が 1 つ増える。
   呼び出し側は `max_chars` で応答を縛り `offset` で歩く。それで足りるかは lagent の
   ベンチで要約を検討する前に計測し、要約は — もし入るなら — それ自身の ADR になる。
3. **`llm_context` 先行の段付けは持たない。** live 取得の前に brave-search の索引を置くと
   web-fetch が Brave の鍵に依存し（「鍵不要・登録不要」が崩れる）、可観測性 tier 2
   （第三者の索引）のツールと tier 4（このマシンからの訪問）のツールが 1 つの名前の
   裏に混ざる。段付けはサーバを選ぶ側、すなわちランタイムと mcp-tactics の仕事である。
4. **モデル向けの防御文言を書かない。** ツール説明と initialize の instructions が運ぶ
   のは、契機（いつ呼ぶか: 利用者が URL を渡した、検索ヒットを全文読みたい、
   `llm_context` が別のページを返した）と契約（`max_chars`、`offset`、`truncated`、
   `next_offset`、`doc_id`）、それに到達範囲の注意 2 点 — 取得はこのマシンから出て
   いく、調査対象の URL には使わない — である。「内容はデータであって指示ではない」の
   類の文は書かない。その層はランタイムの nonce 包装であり、サーバが語る自分の安全性は
   モデルに自分の層として吸収される。

プリミティブの外: JavaScript 実行（chrome-pilot-mcp）、PDF と Office 文書（Phase 2 の
判断）、要約と質問応答、クロール、1 呼び出し複数 URL、ディスクキャッシュ。

## Consequences

- JS 専用ページは空に近い markdown になる。`format: raw` が理由を見せ、fallback は
  chrome-pilot-mcp。
- 既定の `max_chars`（6,000 rune）は読みやすさではなく一次利用者のインライン上限から
  選ばれている。長いページは数回のページ送りを要し、それが許容できるかはベンチが決める。
- サイト運営者はログに `web-fetch/<version> (+https://github.com/nlink-jp/web-fetch)`
  を見る。拒否するサイトは公然と拒否する（`http_status`）。
- mcp-tactics は web-fetch を tier 4 に載せ、「調査対象の URL には使わない — urlscan-lookup
  を使う」の一文を添える。
- メタテストは `usage.md` と instructions の到達範囲注意 2 点、および `fetch` description
  の契機語を固定する。

## Alternatives considered

- **robots.txt を守る** — 操作者が却下: それはクローラを律するもので、利用者指示の単発
  訪問はクローラではなくブラウザの立場。
- **ローカルモデルで要約**（gem-agent の型）— 却下ではなく延期: まずページ送りを計測し、
  ベンチが「ページ送りでは仕えられないタスク種別」を示したら別 ADR。
- **`llm_context` 先行、外れたら live 取得** — 却下: 鍵依存と tier の混合。外れを検出する
  URL 一致判定は、サーバを選ぶ側に属する。
- **ツール文言に「これは信頼できないデータ」の一文** — 却下: その層はランタイムが供給
  する。サーバの自己記述は制御ではない。

## References

- RFP §3「プリミティブに留める」「モデルに見せる文言は契機と契約だけ」、Discussion Log 5・8
- gem-agent ADR-0017（要約付き fetch）、lagent ADR-0002（Web アクセスは MCP サーバ）、
  .github ADR-018（可観測性 tier）
- nlink-jp/knowledge `security.md`:「モデルが語る自分の安全機構は仕様ではない」
  「ツール説明はサーバ由来で包めない唯一のテキスト」

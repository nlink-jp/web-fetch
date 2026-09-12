# ADR-0003: golang.org/x/net と golang.org/x/text は一次提供物の依存として扱う

| Field | Value |
|-------|-------|
| Status | **Accepted** |
| Date | 2026-09-12 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers |
| Triggered by | 標準ライブラリには HTML tokenizer もレガシー文字コードのデコーダも無い。組織のサプライチェーン規則は stdlib・ベンダー一次 SDK・REST 直叩きを認めコミュニティ製を退けるが、`golang.org/x/` の位置を決めていない |

## Context

ページの本文抽出には HTML の tokenizer とパーサが要る。日本語ページを読むには
Shift_JIS・EUC-JP・ISO-2022-JP のデコーダが要る。どちらも Go の標準ライブラリに無い。
archive の agentic-web-search は正規表現でマークアップを剥がし 8,000 バイトで切ることで
依存を避けたが、その結果は RFP が「繰り返さないもの」として名指しするほど粗かった。

組織の規則（stdlib → ベンダー一次 → REST 直叩き。コミュニティ製は決して採らず自前で
再実装）は API クライアントのために書かれた。Go チーム自身のリポジトリである
`golang.org/x/` は名指しされていない。標準ライブラリとエコシステムの間に位置し、著者と
レビュー過程は標準ライブラリと同じで、互換性保証の外でリリースされる。組織内の大半の
Go リポジトリは Google の SDK 経由で `x/net` と `x/text` を間接依存として既に持ち、
mail-analyzer・mail-analyzer-local・lite-rag は `x/text` に直接依存している。`x/net/html`
や `x/text/encoding` を直接 import するリポジトリはまだ無い。

## Decision

1. `golang.org/x/net`（`html`、`html/atom`、`html/charset`）と `golang.org/x/text`
   （`encoding`、`encoding/htmlindex`、およびそれが引くデコーダ群）は web-fetch の
   **一次提供物の依存**であり、サプライチェーン規則上はベンダー SDK と同じ立場に置く:
   Go チームが書き保守し、Go モジュールミラーからチェックサムデータベース検証付きで
   配布され、`go.sum` で固定される。
2. これらが**唯一の**非標準ライブラリ依存である。HTML→Markdown 変換器も readability
   移植も文字コード検出器もエコシステムから採らない。抽出規則はパーサの DOM に対して
   ここ（`internal/extract`）に書く。
3. 版は固定し、意図して上げる（`go get golang.org/x/net@latest` を単独コミットで）。
   リリース手順に `govulncheck` を含める。`charset.DetermineEncoding` の順序や
   `html.Parse` の木の形を変える更新は extract のフィクスチャが捕まえる。

## Consequences

- `go.mod` に `require` が 2 行、`go.sum` にそのチェックサムが入る。brave-search の
  「`require` ブロック無し」の記述は引き継がれない。AGENTS.md と CLAUDE.md は 2 つの
  モジュール名と理由を書く。
- web-fetch が報告する文字コード判定順（`charset_source`）はライブラリの WHATWG 順
  （BOM → ヘッダ → meta → UTF-8 妥当性 → windows-1252）であり、RFP の側を合わせて
  訂正した。
- 3 つ目の依存の要望は、それが標準ライブラリか、`golang.org/x/` か、ここに書くものか
  を問うことから始まる。
- この分類は次に `x/` が要るツールが再論しないために記録するが、拘束するのは web-fetch
  だけであり、組織規則は変わらない。

## Alternatives considered

- **マークアップへの正規表現**（archive のコード）— 却下: 実ページで健全でない（`>` を
  含む属性、マークアップを含むスクリプト、実体参照）。本文根を選ぶこともできない。
- **コミュニティ製 HTML→Markdown ライブラリ** — サプライチェーン規則で却下。
- **自前の tokenizer** — 却下: `x/net/html` の WHATWG 準拠 tokenizer を再実装するのは
  数週間の仕事で、Go チームのものより低い品質にしか届かない。
- **UTF-8 のみ、レガシー文字コードは Phase 2** — 操作者が却下: 日本語サイトは今も
  Shift_JIS を配信しており、デコーダはパーサの charset 補助が既に必要とする同じ `x/text`
  モジュールにある。

## References

- RFP §2「External Dependencies」、§3「なぜ Go / stdlib + golang.org/x か」
- 組織規則: 依存の選択は stdlib → 一次 SDK → REST。コミュニティ製は採らない
  （保守者のフィードバック記録、2026-09-05）
- nlink-jp/knowledge `build-and-packaging.md` — 取得物の固定とチェックサム検証について

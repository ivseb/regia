// Lo script che conta i testi fuori dal catalogo (scripts/check-i18n.mjs): `pnpm test`.
import { test } from "node:test";
import assert from "node:assert/strict";
// @ts-expect-error modulo JavaScript senza tipi
import { findTexts } from "../../scripts/check-i18n.mjs";

const texts = (code: string): string[] => findTexts(code).map((t: { text: string }) => t.text);

test("conta il testo nel JSX e negli attributi visibili", () => {
  const code = `export const A = () => (<div className="flex gap-2" title="Chiudi la finestra"><p>Salva le modifiche</p><input placeholder="Cerca" aria-label="Cerca" /></div>);`;
  assert.deepEqual(texts(code), ["Chiudi la finestra", "Salva le modifiche", "Cerca", "Cerca"]);
});

test("non conta t(), classi, acronimi, numeri, confronti e chiavi", () => {
  const code = `
    export function A({ s }: { s: string }) {
      const t = useT();
      return (
        <section className="border-b border-hair" data-testid="stato-vuoto" title={t("x.titolo")}>
          <h1>{t("auth.login.title")}</h1>
          <span>DORA · 12</span>
          {s === "Bozza" && <b className={s === "Approvata" ? "text-ok" : ""}>{s}</b>}
          {items.map((i) => <li key={i.id}>{i.name}</li>)}
        </section>
      );
    }`;
  assert.deepEqual(texts(code), []);
});

test("conta i testi dentro le espressioni del JSX e nei template", () => {
  const code = "export const A = ({ n }: { n: number }) => <p>{n > 0 ? \"Aperta ora\" : \"Chiusa\"} <i title={`Scade il ${d}`}>x</i></p>;";
  assert.deepEqual(texts(code), ["Aperta ora", "Chiusa", "Scade il"]);
});

test("generici, espressioni regolari e confronti non sono JSX", () => {
  const code = `const [a, setA] = useState<string>(""); const ok = a.replace(/["']/g, "") && 1 < 2; const m = new Map<string, number>();`;
  assert.deepEqual(texts(code), []);
});

test("l'eccezione motivata «i18n-ok» esclude il testo", () => {
  const code = `export const A = () => (\n  <div>\n    {/* i18n-ok: sigla */}\n    <code>Authorization: Bearer</code>\n  </div>\n);`;
  assert.deepEqual(texts(code), []);
});

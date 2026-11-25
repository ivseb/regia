// Tipi del catalogo dei testi. Un testo è una stringa con segnaposto {nome}, oppure un
// insieme di forme di plurale scelte con Intl.PluralRules (il parametro è sempre {count}).
export type Plural = { zero?: string; one: string; two?: string; few?: string; many?: string; other: string };
export type Entry = string | Plural;
export type Source = Record<string, Entry>;

/** La traduzione ha le stesse chiavi della sorgente italiana, e le stesse forme (testo o plurale). */
export type Translation<S extends Source> = { [K in keyof S]: S[K] extends string ? string : Plural };

export type Lang = "it" | "en";
export type Params = Record<string, string | number>;

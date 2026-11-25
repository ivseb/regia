-- Stima della frequenza di una ricerca full-text (benchmark del 2026-11-24).
-- Serve all'app per scegliere la strategia senza tentativi: termine frequente => si scorre
-- l'indice ordinato per data (la pagina si riempie subito); termine raro => indice GIN
-- tramite fts_match. La stima usa le statistiche di ANALYZE sui lessemi più comuni della
-- colonna elements.search (le stesse che userebbe il pianificatore se la RLS non gli
-- impedisse l'indice GIN). Parola assente dalle statistiche => rara (frequenza 0).
-- Frequenza della query = la minore tra le sue parole (sono in AND); per ogni parola
-- vale la maggiore tra la forma semplice e la radice italiana.
create or replace function fts_frequency(p_query text) returns real
language sql stable security definer set search_path = public, pg_catalog as $$
  with st as (
    select most_common_elems::text::text[] as elems, most_common_elem_freqs as freqs
    from pg_stats
    where schemaname = 'public' and tablename = 'elements' and attname = 'search'
    limit 1
  ),
  words as (
    select distinct w from regexp_split_to_table(lower(unaccent(coalesce(p_query, ''))), '[^[:alnum:]]+') w
    where length(w) > 1
  ),
  per_word as (
    select w,
           greatest(
             coalesce((select st.freqs[array_position(st.elems, w)] from st), 0),
             coalesce((select max(st.freqs[array_position(st.elems, l)])
                       from st, unnest(tsvector_to_array(to_tsvector('italian', w))) l), 0)
           ) as f,
           exists (select 1 from unnest(tsvector_to_array(to_tsvector('italian', w)))) as significant
    from words
  )
  select coalesce(min(f) filter (where significant), 0)::real from per_word
$$;
revoke all on function fts_frequency(text) from public;
grant execute on function fts_frequency(text) to mytrack_app;

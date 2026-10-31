package substratereview

import "encoding/json"

// Batches splits the review into one judge dispatch per area, in Areas order,
// so each judge's manifest stays focused. The constitution rules ride the
// principles batch. An area whose manifest would exceed maxManifestBytes is
// split into parts that fit (a single item that alone exceeds it is still one
// part — the dispatch budget check then refuses it as a batch failure);
// maxManifestBytes <= 0 never splits. An area with nothing to judge yields no
// batch.
func Batches(in JudgeInput, maxManifestBytes int) []JudgeInput {
	byArea := map[string][]JudgeItem{}
	for _, it := range in.Items {
		a := AreaOf(it.Kind)
		byArea[a] = append(byArea[a], it)
	}
	var out []JudgeInput
	for _, area := range Areas {
		b := JudgeInput{Area: area, Scratch: in.Scratch, Items: byArea[area],
			ConstitutionPath: in.ConstitutionPath, OrchestratorModel: in.OrchestratorModel}
		if area == AreaPrinciples {
			b.Rules = in.Rules
		}
		if len(b.Items) == 0 && len(b.Rules) == 0 {
			continue
		}
		parts := split(b, maxManifestBytes)
		if len(parts) > 1 {
			for i := range parts {
				parts[i].Chunk = i + 1
			}
		}
		out = append(out, parts...)
	}
	return out
}

// split halves b until each part's manifest fits max.
func split(b JudgeInput, max int) []JudgeInput {
	if max <= 0 || len(b.Items) < 2 || manifestSize(b) <= max {
		return []JudgeInput{b}
	}
	mid := len(b.Items) / 2
	first, second := b, b
	first.Items, second.Items = b.Items[:mid:mid], b.Items[mid:]
	second.Rules = nil
	return append(split(first, max), split(second, max)...)
}

func manifestSize(b JudgeInput) int {
	raw, _ := json.Marshal(ManifestOf(b))
	return len(raw)
}

package normalize

import (
	"strconv"
	"strings"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/profile"
)

func applyPhases(snapshot *model.SourceSnapshot, traces map[string]Trace, raw driver.RawData, p *profile.Profile) {
	if phases := p.Phases; phases != nil {
		names := []string{"input_phases", "output_phases"}
		if phases.From == nil {
			for i, count := range []int{phases.Input, phases.Output} {
				if count == 0 {
					continue
				}
				snapshot.Fields[names[i]] = model.Field{Num: float64(count), Quality: model.Good}
				traces[names[i]] = Trace{Field: names[i], Source: "profile:phases", Decoded: count}
			}
		} else {
			rawValue, quality, reason := extract(raw, profile.Mapping{Source: *phases.From, Type: "string"})
			counts := []int{0, 0}
			if quality == model.Good {
				text, ok := rawValue.(string)
				if !ok {
					quality, reason = model.Invalid, model.ReasonDecodeError
				} else {
					text = strings.TrimSpace(text)
					if text == "" || text == "---" {
						quality, reason = model.Invalid, model.ReasonPlaceholder
					} else {
						pieces := strings.Split(text, "/")
						if len(pieces) != 2 {
							quality, reason = model.Invalid, model.ReasonDecodeError
						} else {
							for i, piece := range pieces {
								count, err := strconv.Atoi(piece)
								if err != nil || count < 1 || count > 3 {
									quality, reason = model.Invalid, model.ReasonDecodeError
									break
								}
								counts[i] = count
							}
						}
					}
				}
			}
			for i, name := range names {
				field := model.Field{Quality: quality, Reason: reason}
				if quality == model.Good {
					field.Num = float64(counts[i])
				}
				snapshot.Fields[name] = field
				traces[name] = Trace{Field: name, Source: sourceName(*phases.From), Raw: rawValue, Decoded: field.Num}
			}
		}
	}
	for key, field := range snapshot.Fields {
		phase := model.Phase(key)
		if phase == 0 {
			continue
		}
		spec, _ := model.Lookup(key)
		count := snapshot.Fields["input_phases"]
		if spec.Group == "output" {
			count = snapshot.Fields["output_phases"]
		}
		if count.Quality == model.Good {
			if phase > int(count.Num) {
				snapshot.Fields[key] = bad(spec, model.Unsupported, model.ReasonPhaseInactive)
			}
		} else if count.Quality == model.Invalid {
			// 相数错误不能把未知的相解释为有意义的零；保留数值在 Trace 中用于排查。
			snapshot.Fields[key] = bad(spec, model.Invalid, count.Reason)
		} else if phase > 1 && field.Quality == model.Good {
			snapshot.Fields[key] = bad(spec, model.Unsupported, model.ReasonNotMapped)
		}
	}
}

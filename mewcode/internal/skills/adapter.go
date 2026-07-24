package skills

// SkillCatalogItem prompt 包可用的 Skill 目录条目。
// prompt 包定义同名字段（避免循环依赖）。
type SkillCatalogItem struct {
	Name        string
	Description string
}

// ToPromptItems 将 Catalog 内所有 Skill 转换为 prompt 包可用的目录列表。
func (c *Catalog) ToPromptItems() []SkillCatalogItem {
	list := c.List()
	result := make([]SkillCatalogItem, len(list))
	for i, sk := range list {
		result[i] = SkillCatalogItem{
			Name:        sk.Meta.Name,
			Description: sk.Meta.Description,
		}
	}
	return result
}

package channel

// Chat01DefaultContextWindow 是运营保守默认值，管理员可在模型能力中覆盖。
// Chat01 运行独立的服务端 Agent，不继承同名基础模型的目录规格。
const Chat01DefaultContextWindow = 70_000

// Chat01DefaultCapabilitiesJSON 用于从 Chat01 路由新建的专用平台模型。
const Chat01DefaultCapabilitiesJSON = `{"contextWindow":70000,"inputModalities":["text","image","attachment"]}`

// Chat01InputModalities 返回路由适配器支持的默认输入类别。
// attachment 的原生发送仍须由实际 Chat01 adapter 与有效模型声明共同授权。
func Chat01InputModalities() []string {
	return []string{InputModalityText, InputModalityImage, InputModalityAttachment}
}

// ResolveChat01ModelCaps 只使用显式覆盖和 Chat01 默认，不从模型名称猜测上游能力。
func ResolveChat01ModelCaps(capabilitiesJSON string) ResolvedModelCaps {
	return ResolveModelCapsFromCapabilitiesWithFallback("", capabilitiesJSON, Chat01DefaultContextWindow)
}

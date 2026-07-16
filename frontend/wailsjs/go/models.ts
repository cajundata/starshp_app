export namespace appapi {
	
	export class EventDTO {
	    id: string;
	    turnId: string;
	    runId?: string;
	    personaId?: string;
	    modelId?: string;
	    kind: string;
	    text?: string;
	    toolCallId?: string;
	    toolName?: string;
	    toolInput?: number[];
	    toolMetadata?: number[];
	    toolLatencyMs?: number;
	    isError?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EventDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turnId = source["turnId"];
	        this.runId = source["runId"];
	        this.personaId = source["personaId"];
	        this.modelId = source["modelId"];
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.toolCallId = source["toolCallId"];
	        this.toolName = source["toolName"];
	        this.toolInput = source["toolInput"];
	        this.toolMetadata = source["toolMetadata"];
	        this.toolLatencyMs = source["toolLatencyMs"];
	        this.isError = source["isError"];
	    }
	}

}

export namespace library {
	
	export class Item {
	    filename: string;
	    name: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filename = source["filename"];
	        this.name = source["name"];
	        this.error = source["error"];
	    }
	}

}

export namespace persona {
	
	export class Persona {
	    id: string;
	    name: string;
	    model: string;
	    color: string;
	    tools?: string[];
	    library?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Persona(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.model = source["model"];
	        this.color = source["color"];
	        this.tools = source["tools"];
	        this.library = source["library"];
	    }
	}

}

export namespace pipeline {
	
	export class DueReviewView {
	    CriterionID: string;
	    IdeaID: string;
	    IdeaTitle: string;
	    IdeaStatus: string;
	    Metric: string;
	    Threshold: string;
	    ReviewDate: number;
	    OnMiss: string;
	    DaysOverdue: number;
	
	    static createFrom(source: any = {}) {
	        return new DueReviewView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.CriterionID = source["CriterionID"];
	        this.IdeaID = source["IdeaID"];
	        this.IdeaTitle = source["IdeaTitle"];
	        this.IdeaStatus = source["IdeaStatus"];
	        this.Metric = source["Metric"];
	        this.Threshold = source["Threshold"];
	        this.ReviewDate = source["ReviewDate"];
	        this.OnMiss = source["OnMiss"];
	        this.DaysOverdue = source["DaysOverdue"];
	    }
	}

}

export namespace provider {
	
	export class ModelInfo {
	    display: string;
	    id: string;
	    provider: string;
	    maxContext?: number;
	    baseURL?: string;
	    apiKeyEnv?: string;
	    reasoningEffort?: string;
	    inputModalities?: string[];
	    outputModalities?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display = source["display"];
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.maxContext = source["maxContext"];
	        this.baseURL = source["baseURL"];
	        this.apiKeyEnv = source["apiKeyEnv"];
	        this.reasoningEffort = source["reasoningEffort"];
	        this.inputModalities = source["inputModalities"];
	        this.outputModalities = source["outputModalities"];
	    }
	}

}

export namespace store {
	
	export class Conversation {
	    id: string;
	    title: string;
	    createdAt: number;
	    updatedAt: number;
	    pinnedModel: string;
	    pinnedPersona: string;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.pinnedModel = source["pinnedModel"];
	        this.pinnedPersona = source["pinnedPersona"];
	    }
	}
	export class Idea {
	    ID: string;
	    Title: string;
	    Summary: string;
	    Pathway: string;
	    Status: string;
	    KillReason: string;
	    FinancialFlag: boolean;
	    Source: string;
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Idea(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Title = source["Title"];
	        this.Summary = source["Summary"];
	        this.Pathway = source["Pathway"];
	        this.Status = source["Status"];
	        this.KillReason = source["KillReason"];
	        this.FinancialFlag = source["FinancialFlag"];
	        this.Source = source["Source"];
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
	    }
	}
	export class KillCriterion {
	    ID: string;
	    IdeaID: string;
	    ReviewID: string;
	    Metric: string;
	    Threshold: string;
	    ReviewDate: number;
	    OnMiss: string;
	    Status: string;
	    Notes: string;
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new KillCriterion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.IdeaID = source["IdeaID"];
	        this.ReviewID = source["ReviewID"];
	        this.Metric = source["Metric"];
	        this.Threshold = source["Threshold"];
	        this.ReviewDate = source["ReviewDate"];
	        this.OnMiss = source["OnMiss"];
	        this.Status = source["Status"];
	        this.Notes = source["Notes"];
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
	    }
	}
	export class Message {
	    id: string;
	    conversationId: string;
	    role: string;
	    content: string;
	    model: string;
	    createdAt: number;
	    ragContext: string;
	    ragSources: string;
	    inputTokens?: number;
	    outputTokens?: number;
	    cachedInputTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.conversationId = source["conversationId"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.model = source["model"];
	        this.createdAt = source["createdAt"];
	        this.ragContext = source["ragContext"];
	        this.ragSources = source["ragSources"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedInputTokens = source["cachedInputTokens"];
	    }
	}
	export class StatusChange {
	    ID: string;
	    IdeaID: string;
	    FromStatus: string;
	    ToStatus: string;
	    Reason: string;
	    CreatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new StatusChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.IdeaID = source["IdeaID"];
	        this.FromStatus = source["FromStatus"];
	        this.ToStatus = source["ToStatus"];
	        this.Reason = source["Reason"];
	        this.CreatedAt = source["CreatedAt"];
	    }
	}
	export class TextbookScope {
	    name: string;
	    chapters: number[];
	
	    static createFrom(source: any = {}) {
	        return new TextbookScope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.chapters = source["chapters"];
	    }
	}

}

export namespace textbooks {
	
	export class Chapter {
	    num: number;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new Chapter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.num = source["num"];
	        this.path = source["path"];
	    }
	}
	export class Book {
	    name: string;
	    chapters: Chapter[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Book(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.chapters = this.convertValues(source["chapters"], Chapter);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}


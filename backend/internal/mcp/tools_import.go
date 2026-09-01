package mcp

import (
	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

func newImportTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "upload_text_document",
			Description: "向指定知识库上传纯文本文档。参数 knowledgeBaseId、fileName、content 为必填，仅支持 .txt/.md/.csv。",
			InputSchema: objectSchema(
				map[string]any{
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "文件名，需带扩展名"},
					"content":         map[string]any{"type": "string", "description": "纯文本内容"},
				},
				[]string{"knowledgeBaseId", "fileName", "content"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				fileName, err := requiredStringArg(args, "fileName")
				if err != nil {
					return ToolCallResult{}, err
				}
				content, err := requiredStringArg(args, "content")
				if err != nil {
					return ToolCallResult{}, err
				}
				if int64(len(content)) > maxInlineUploadBytes {
					return ToolCallResult{}, fmt.Errorf("inline text upload too large: current=%s, max=%s; please POST file stream to /api/uploads first, then call register_staged_upload", util.FormatFileSize(int64(len(content))), util.FormatFileSize(maxInlineUploadBytes))
				}
				if err := validateTextUploadFileName(fileName, appService.GetConfig()); err != nil {
					return ToolCallResult{}, err
				}
				staged, err := stageInlineUploadAs(appService, fileName, []byte(content), "mcp-text", caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				uploaded, err := registerStagedUploadAs(appService, ctx, staged.ID, knowledgeBaseID, fileName, caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文本文档《%s》上传成功。", uploaded.Name),
					map[string]any{"uploaded": buildSafeMCPDocument(uploaded), "knowledgeBaseId": uploaded.KnowledgeBaseID, "stagedUploadId": staged.ID},
				), nil
			},
		},
		{
			Name:        "upload_document",
			Description: "向指定知识库上传文档。参数 knowledgeBaseId、fileName、contentBase64 为必填。仅适用于小文件，大文件请先走 HTTP /api/uploads 暂存再调用 register_staged_upload。",
			InputSchema: objectSchema(
				map[string]any{
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "文件名，需带扩展名"},
					"contentBase64":   map[string]any{"type": "string", "description": "文件内容的 Base64 编码"},
				},
				[]string{"knowledgeBaseId", "fileName", "contentBase64"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				fileName, err := requiredStringArg(args, "fileName")
				if err != nil {
					return ToolCallResult{}, err
				}
				contentBase64, err := requiredStringArg(args, "contentBase64")
				if err != nil {
					return ToolCallResult{}, err
				}
				if err := validateUploadFileName(fileName, appService.GetConfig()); err != nil {
					return ToolCallResult{}, err
				}
				decoded, err := base64.StdEncoding.DecodeString(contentBase64)
				if err != nil {
					return ToolCallResult{}, errInvalidContentBase64(fileName)
				}
				if len(decoded) == 0 {
					return ToolCallResult{}, fmt.Errorf("decoded file content is empty")
				}
				if int64(len(decoded)) > maxInlineUploadBytes {
					return ToolCallResult{}, fmt.Errorf("inline upload too large: current=%s, max=%s; please POST file stream to /api/uploads first, then call register_staged_upload", util.FormatFileSize(int64(len(decoded))), util.FormatFileSize(maxInlineUploadBytes))
				}
				staged, err := stageInlineUploadAs(appService, fileName, decoded, "mcp-inline", caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				uploaded, err := registerStagedUploadAs(appService, ctx, staged.ID, knowledgeBaseID, fileName, caller)
				if err != nil {
					return ToolCallResult{}, wrapBinaryUploadParseError(fileName, err)
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》上传成功。", uploaded.Name),
					map[string]any{"uploaded": buildSafeMCPDocument(uploaded), "knowledgeBaseId": uploaded.KnowledgeBaseID, "stagedUploadId": staged.ID},
				), nil
			},
		},
		{
			Name:        "register_staged_upload",
			Description: "基于已暂存的 uploadId 将文件注册到指定知识库。适合大文件上传场景。参数 uploadId、knowledgeBaseId 为必填，fileName 为选填。",
			InputSchema: objectSchema(
				map[string]any{
					"uploadId":        map[string]any{"type": "string", "description": "通过 HTTP /api/uploads 返回的上传 ID"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "可选，注册后的文件名"},
				},
				[]string{"uploadId", "knowledgeBaseId"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				uploadID, err := requiredStringArg(args, "uploadId")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				fileName := optionalStringArg(args, "fileName")
				uploaded, err := registerStagedUploadAs(appService, ctx, uploadID, knowledgeBaseID, fileName, caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("暂存文件《%s》已注册到知识库。", uploaded.Name),
					map[string]any{"uploaded": buildSafeMCPDocument(uploaded), "knowledgeBaseId": uploaded.KnowledgeBaseID, "uploadId": uploadID},
				), nil
			},
		},
		{
			Name:        "start_import_job",
			Description: "启动异步长任务。jobType 可选：import（默认）、reindex、eval_dataset、batch_index；不同类型使用对应参数。",
			InputSchema: objectSchema(
				map[string]any{
					"jobType":         map[string]any{"type": "string", "enum": []string{"import", "reindex", "eval_dataset", "batch_index"}, "description": "任务类型，默认为 import"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "文件名，需带扩展名"},
					"content":         map[string]any{"type": "string", "description": "纯文本内容；留空会创建失败状态用于排查"},
					"documentId":      map[string]any{"type": "string", "description": "reindex 类型的文档 ID"},
					"maxPerDocument":  map[string]any{"type": "integer", "description": "eval_dataset 类型每个文档最多生成多少条，默认 5，最大 20"},
					"uploadIds":       map[string]any{"type": "array", "description": "batch_index 类型的暂存上传 ID 列表", "items": map[string]any{"type": "string"}},
					"concurrency":     map[string]any{"type": "integer", "description": "batch_index 类型并发数，默认 3，最大 10"},
				},
				[]string{},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				jobType := strings.ToLower(optionalStringArg(args, "jobType"))
				if jobType == "" {
					jobType = "import"
				}
				knowledgeBaseID := optionalStringArg(args, "knowledgeBaseId")
				fileName := optionalStringArg(args, "fileName")
				documentID := optionalStringArg(args, "documentId")
				uploadIDs, err := optionalStringSliceArg(args, "uploadIds")
				if err != nil {
					return ToolCallResult{}, err
				}
				switch jobType {
				case "import":
					if knowledgeBaseID == "" || fileName == "" {
						return ToolCallResult{}, fmt.Errorf("knowledgeBaseId and fileName are required for import jobs")
					}
				case "reindex":
					if knowledgeBaseID == "" || documentID == "" {
						return ToolCallResult{}, fmt.Errorf("knowledgeBaseId and documentId are required for reindex jobs")
					}
				case "eval_dataset":
				case "batch_index":
					if knowledgeBaseID == "" || len(uploadIDs) == 0 {
						return ToolCallResult{}, fmt.Errorf("knowledgeBaseId and uploadIds are required for batch_index jobs")
					}
				default:
					return ToolCallResult{}, fmt.Errorf("unsupported jobType: %s", jobType)
				}
				job, err := startMCPImportJobAs(appService, model.MCPStartImportJobRequest{
					KnowledgeBaseID: knowledgeBaseID,
					FileName:        fileName,
					Content:         optionalStringArg(args, "content"),
					JobType:         jobType,
					DocumentID:      documentID,
					MaxPerDocument:  optionalIntArg(args, "maxPerDocument"),
					UploadIDs:       uploadIDs,
					Concurrency:     optionalIntArg(args, "concurrency"),
				}, caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("%s 任务已启动：%s。", mcpJobTypeLabel(jobType), job.ID),
					map[string]any{"job": job},
				), nil
			},
		},
		{
			Name:            "get_job_status",
			Description:     "查询 MCP 长任务状态。参数 jobId 为必填。",
			InputSchema:     requiredStringPropertySchema("jobId", "Job ID"),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				jobID, err := requiredStringArg(args, "jobId")
				if err != nil {
					return ToolCallResult{}, err
				}
				job, err := getMCPJobStatusAs(appService, jobID, caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				job = sanitizeMCPJob(job)
				return NewTextResult(
					fmt.Sprintf("任务 %s 当前状态为 %s，进度 %d%%。", job.ID, job.Status, job.Progress),
					map[string]any{"job": job},
				), nil
			},
		},
		{
			Name:            "cancel_job",
			Description:     "取消 MCP 长任务。参数 jobId 为必填。",
			InputSchema:     requiredStringPropertySchema("jobId", "Job ID"),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				jobID, err := requiredStringArg(args, "jobId")
				if err != nil {
					return ToolCallResult{}, err
				}
				job, err := cancelMCPJobAs(appService, jobID, caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				job = sanitizeMCPJob(job)
				return NewTextResult(
					fmt.Sprintf("任务 %s 当前状态为 %s。", job.ID, job.Status),
					map[string]any{"job": job},
				), nil
			},
		},
		{
			Name:            "retry_job",
			Description:     "显式重试已失败的 MCP 长任务。只允许重试失败状态，最多重试 3 次；参数 jobId 为必填。",
			InputSchema:     requiredStringPropertySchema("jobId", "Job ID"),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				jobID, err := requiredStringArg(args, "jobId")
				if err != nil {
					return ToolCallResult{}, err
				}
				job, err := retryMCPJobAs(appService, jobID, caller)
				if err != nil {
					return ToolCallResult{}, err
				}
				job = sanitizeMCPJob(job)
				return NewTextResult(
					fmt.Sprintf("失败任务 %s 已重新启动，新任务为 %s。", jobID, job.ID),
					map[string]any{"job": job, "sourceJobId": job.ParentJobID},
				), nil
			},
		},
		{
			Name:        "list_recent_jobs",
			Description: "列出最近 MCP 长任务。参数 limit 可选，默认 20。",
			InputSchema: objectSchema(
				map[string]any{
					"limit": map[string]any{"type": "integer", "description": "最多返回多少个 job，默认 20，最大 20"},
				},
				[]string{},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				caller := principalFromContext(ctx)
				jobs := listRecentMCPJobsAs(appService, optionalIntArg(args, "limit"), caller)
				for index := range jobs {
					jobs[index] = sanitizeMCPJob(jobs[index])
				}
				return NewTextResult(formatMCPJobListText(jobs), map[string]any{"jobs": jobs}), nil
			},
		},
	}
}
